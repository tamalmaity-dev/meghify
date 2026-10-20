package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// JobRepo is the durable queue that replaces Redis + Horizon.
//
// A job is claimed with an atomic UPDATE guarded by the status column, so any
// number of panel replicas can share the queue without a lock service.
type JobRepo struct{ db *sql.DB }

// Enqueue inserts a job.
func (r *JobRepo) Enqueue(ctx context.Context, kind, payload string, runAt time.Time, maxAttempts int) (*Job, error) {
	now := time.Now().UTC()
	j := &Job{
		ID:          NewID(),
		Kind:        kind,
		Payload:     payload,
		Status:      JobPending,
		Attempts:    0,
		MaxAttempts: maxAttempts,
		RunAt:       runAt,
		CreatedAt:   now,
	}
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO jobs (id, kind, payload, status, attempts, max_attempts, run_at, locked_at, last_error, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		j.ID, j.Kind, j.Payload, j.Status, j.Attempts, j.MaxAttempts,
		Text(j.RunAt), "", "", Text(j.CreatedAt)); err != nil {
		return nil, fmt.Errorf("enqueue %s: %w", kind, err)
	}
	return j, nil
}

// EnqueueUnique enqueues a job only if no pending job of the same kind and
// payload exists. Used to collapse duplicate webhook deliveries.
func (r *JobRepo) EnqueueUnique(ctx context.Context, kind, payload string, runAt time.Time, maxAttempts int) (*Job, bool, error) {
	var existing string
	err := r.db.QueryRowContext(ctx,
		`SELECT id FROM jobs WHERE kind = ? AND payload = ? AND status IN (?, ?, ?) LIMIT 1`,
		kind, payload, JobPending, JobRunning, JobRetrying).Scan(&existing)
	switch {
	case err == nil:
		return nil, false, nil
	case !errors.Is(err, sql.ErrNoRows):
		return nil, false, err
	}
	j, err := r.Enqueue(ctx, kind, payload, runAt, maxAttempts)
	return j, true, err
}

// Claim atomically takes the oldest claimable job. The follow-up UPDATE is
// guarded by the status the worker read, so concurrent workers cannot both win.
func (r *JobRepo) Claim(ctx context.Context) (*Job, error) {
	now := time.Now().UTC()
	// A job stuck in "retrying" with a lock older than the lease is reclaimable.
	staleLease := now.Add(-5 * time.Minute)

	j, err := r.nextClaimable(ctx, now, staleLease)
	if err != nil || j == nil {
		return nil, err
	}

	res, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status = ?, locked_at = ?, started_at = ?, attempts = attempts + 1
		  WHERE id = ? AND status = ?`,
		JobRunning, Text(now), Text(now), j.ID, j.Status)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, nil
	}
	j.Status = JobRunning
	j.LockedAt = &now
	j.StartedAt = &now
	j.Attempts++
	return j, nil
}

func (r *JobRepo) nextClaimable(ctx context.Context, now, staleLease time.Time) (*Job, error) {
	j, err := scanJob(r.db.QueryRowContext(ctx, claimQuery,
		Text(now), JobPending, JobRetrying, Text(staleLease)))
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return j, err
}

// claimQuery selects the oldest job that is due and not currently running.
const claimQuery = `SELECT id, kind, payload, status, attempts, max_attempts, run_at, locked_at, last_error, created_at, started_at, finished_at
	 FROM jobs
	 WHERE run_at <= ? AND (
	       status = ?
	    OR (status = ? AND locked_at IS NOT NULL AND locked_at < ?)
	 )
	 ORDER BY run_at ASC
	 LIMIT 1`

// Complete marks a job as finished.
func (r *JobRepo) Complete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status = ?, finished_at = ? WHERE id = ?`,
		JobDone, Text(time.Now().UTC()), id)
	return err
}

// Fail records a failure and either schedules a retry or dead-letters the job.
func (r *JobRepo) Fail(ctx context.Context, id, errMsg string) error {
	rows := r.db.QueryRowContext(ctx,
		`SELECT attempts, max_attempts FROM jobs WHERE id = ?`, id)
	var attempts, max int
	if err := rows.Scan(&attempts, &max); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}

	if attempts >= max {
		_, err := r.db.ExecContext(ctx,
			`UPDATE jobs SET status = ?, last_error = ?, finished_at = ?, locked_at = NULL WHERE id = ?`,
			JobFailed, errMsg, Text(time.Now().UTC()), id)
		return err
	}

	// Exponential backoff: 30s, 60s, 120s, ...
	backoff := time.Duration(attempts) * 30 * time.Second
	if backoff < 30*time.Second {
		backoff = 30 * time.Second
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status = ?, last_error = ?, run_at = ?, locked_at = NULL, finished_at = NULL WHERE id = ?`,
		JobRetrying, errMsg, Text(time.Now().UTC().Add(backoff)), id)
	return err
}

// Requeue moves a finished job back to pending. It deliberately refuses to
// touch a running job: a second worker claiming it would execute the same work
// twice. Use RequeueRunning only during startup recovery.
func (r *JobRepo) Requeue(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status = ?, locked_at = NULL, run_at = ?, finished_at = NULL
		  WHERE id = ? AND status IN (?, ?)`,
		JobPending, Text(time.Now().UTC()), id, JobFailed, JobDone)
	if err != nil {
		return err
	}
	return rowsAffected(res)
}

// RequeueRunning is the crash-recovery pass: jobs left running by a process
// that stopped before completing them become pending again. It is called before
// workers start, never while another instance is live.
func (r *JobRepo) RequeueRunning(ctx context.Context) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status = ?, locked_at = NULL, run_at = ?, finished_at = NULL
		  WHERE status = ?`,
		JobPending, Text(time.Now().UTC()), JobRunning)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// JobByID loads a job.
func (r *JobRepo) JobByID(ctx context.Context, id string) (*Job, error) {
	j, err := scanJob(r.db.QueryRowContext(ctx,
		`SELECT id, kind, payload, status, attempts, max_attempts, run_at, locked_at, last_error, created_at, started_at, finished_at
		   FROM jobs WHERE id = ?`, id))
	if err != nil {
		return nil, fmt.Errorf("job %s: %w", id, err)
	}
	return j, nil
}

// Jobs returns jobs filtered by status, newest first.
func (r *JobRepo) Jobs(ctx context.Context, status string, limit int) ([]*Job, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if status == "" {
		rows, err = r.db.QueryContext(ctx,
			`SELECT id, kind, payload, status, attempts, max_attempts, run_at, locked_at, last_error, created_at, started_at, finished_at
			   FROM jobs ORDER BY created_at DESC LIMIT ?`, limit)
	} else {
		rows, err = r.db.QueryContext(ctx,
			`SELECT id, kind, payload, status, attempts, max_attempts, run_at, locked_at, last_error, created_at, started_at, finished_at
			   FROM jobs WHERE status = ? ORDER BY created_at DESC LIMIT ?`, status, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// PendingCount returns how many jobs are waiting.
func (r *JobRepo) PendingCount(ctx context.Context) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jobs WHERE status IN (?, ?)`, JobPending, JobRetrying).Scan(&n)
	return n, err
}

// PruneJobs deletes finished jobs older than the cutoff.
func (r *JobRepo) PruneJobs(ctx context.Context, olderThan time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM jobs WHERE status IN (?, ?) AND created_at < ?`,
		JobDone, JobFailed, Text(olderThan))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PruneJobsByKind deletes finished jobs of one kind older than the cutoff.
// Routine kinds such as host-metrics sampling are pruned aggressively so the
// queue stays a record of meaningful work rather than a telemetry log.
func (r *JobRepo) PruneJobsByKind(ctx context.Context, kind string, olderThan time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM jobs WHERE kind = ? AND status IN (?, ?) AND created_at < ?`,
		kind, JobDone, JobFailed, Text(olderThan))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// JobsExcludingKind lists jobs, newest first, skipping one kind. The queue page
// hides host-metrics rows this way, so telemetry can never bury real work.
func (r *JobRepo) JobsExcludingKind(ctx context.Context, status, excludeKind string, limit int) ([]*Job, error) {
	query := `SELECT id, kind, payload, status, attempts, max_attempts, run_at, locked_at, last_error, created_at, started_at, finished_at
	   FROM jobs WHERE kind != ?`
	args := []any{excludeKind}
	if status != "" {
		query += ` AND status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func scanJob(scanner interface{ Scan(...any) error }) (*Job, error) {
	var j Job
	var runAt, createdAt string
	var lockedAt, startedAt, finishedAt sql.NullString
	err := scanner.Scan(&j.ID, &j.Kind, &j.Payload, &j.Status, &j.Attempts, &j.MaxAttempts,
		&runAt, &lockedAt, &j.LastError, &createdAt, &startedAt, &finishedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, err
	}
	j.RunAt = Parse(runAt)
	j.CreatedAt = Parse(createdAt)
	// locked_at, started_at and finished_at are all nullable: a pending job has
	// none of them, and scanning NULL into a plain string fails.
	j.LockedAt = optionalTime(lockedAt)
	j.StartedAt = optionalTime(startedAt)
	j.FinishedAt = optionalTime(finishedAt)
	return &j, nil
}

// optionalTime converts a nullable timestamp column to a pointer.
func optionalTime(value sql.NullString) *time.Time {
	if !value.Valid || value.String == "" {
		return nil
	}
	at := Parse(value.String)
	return &at
}
