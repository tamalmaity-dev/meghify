package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// DeploymentRepo persists deployment history.
type DeploymentRepo struct{ db *sql.DB }

// CreateDeployment queues a deployment.
func (r *DeploymentRepo) CreateDeployment(ctx context.Context, d *Deployment) error {
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO deployments (id, application_id, status, trigger, commit_sha, commit_message,
		                          image_tag, log_path, error, queued_at, started_at, finished_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.ApplicationID, d.Status, d.Trigger, d.CommitSHA, d.CommitMessage,
		d.ImageTag, d.LogPath, d.Error, Text(d.QueuedAt), TextOptional(d.StartedAt), TextOptional(d.FinishedAt)); err != nil {
		return fmt.Errorf("insert deployment: %w", err)
	}
	return nil
}

const deploymentColumns = `id, application_id, status, trigger, commit_sha, commit_message, image_tag, log_path, error, queued_at, started_at, finished_at`

func scanDeployment(scanner interface{ Scan(...any) error }) (*Deployment, error) {
	var d Deployment
	var queuedAt string
	var startedAt, finishedAt sql.NullString
	err := scanner.Scan(&d.ID, &d.ApplicationID, &d.Status, &d.Trigger, &d.CommitSHA, &d.CommitMessage,
		&d.ImageTag, &d.LogPath, &d.Error, &queuedAt, &startedAt, &finishedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, err
	}
	d.QueuedAt = Parse(queuedAt)
	// A queued deployment has no started_at or finished_at yet.
	d.StartedAt = optionalTime(startedAt)
	d.FinishedAt = optionalTime(finishedAt)
	return &d, nil
}

// DeploymentByID loads a deployment.
func (r *DeploymentRepo) DeploymentByID(ctx context.Context, id string) (*Deployment, error) {
	d, err := scanDeployment(r.db.QueryRowContext(ctx,
		`SELECT `+deploymentColumns+` FROM deployments WHERE id = ?`, id))
	if err != nil {
		return nil, fmt.Errorf("deployment %s: %w", id, err)
	}
	return d, nil
}

// DeploymentsByApplication returns history, newest first.
func (r *DeploymentRepo) DeploymentsByApplication(ctx context.Context, appID string, limit int) ([]*Deployment, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+deploymentColumns+` FROM deployments WHERE application_id = ?
		  ORDER BY queued_at DESC LIMIT ?`, appID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Deployment
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// LatestDeployment returns the most recent deployment for an application, or nil.
func (r *DeploymentRepo) LatestDeployment(ctx context.Context, appID string) (*Deployment, error) {
	d, err := scanDeployment(r.db.QueryRowContext(ctx,
		`SELECT `+deploymentColumns+` FROM deployments WHERE application_id = ?
		  ORDER BY queued_at DESC LIMIT 1`, appID))
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return d, nil
}

// PatchDeployment transitions a deployment to a new phase.
func (r *DeploymentRepo) PatchDeployment(ctx context.Context, id, status string) error {
	res, err := r.db.ExecContext(ctx, `UPDATE deployments SET status = ? WHERE id = ?`, status, id)
	if err != nil {
		return err
	}
	return rowsAffected(res)
}

// StartDeployment marks a deployment as actively running. It only transitions
// a queued deployment, so a cancellation that lands in the queue window is
// never overwritten by a start.
func (r *DeploymentRepo) StartDeployment(ctx context.Context, id, imageTag string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE deployments SET status = ?, image_tag = ?, started_at = ? WHERE id = ? AND status = ?`,
		DeployBuilding, imageTag, Text(time.Now().UTC()), id, DeployQueued)
	if err != nil {
		return err
	}
	return rowsAffected(res)
}

// FinishDeployment records the terminal state of a deployment.
func (r *DeploymentRepo) FinishDeployment(ctx context.Context, id, status, errMsg string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE deployments SET status = ?, error = ?, finished_at = ? WHERE id = ?`,
		status, errMsg, Text(time.Now().UTC()), id)
	if err != nil {
		return err
	}
	return rowsAffected(res)
}

// CancelInFlight marks every queued or running deployment as canceled. It runs
// at startup after the panel was interrupted, so a killed build cannot leave a
// deployment "running" forever (and block the next deploy), and returns the
// affected application IDs so their status can be reconciled.
func (r *DeploymentRepo) CancelInFlight(ctx context.Context, message string) ([]string, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT DISTINCT application_id FROM deployments WHERE status IN (?, ?, ?, ?)`,
		DeployQueued, DeployBuilding, DeployPushing, DeployDeploying)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var appIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		appIDs = append(appIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(appIDs) == 0 {
		return nil, nil
	}

	if _, err := r.db.ExecContext(ctx,
		`UPDATE deployments SET status = ?, error = ?, finished_at = ? WHERE status IN (?, ?, ?, ?)`,
		DeployCanceled, message, Text(time.Now().UTC()),
		DeployQueued, DeployBuilding, DeployPushing, DeployDeploying); err != nil {
		return nil, err
	}
	return appIDs, nil
}

// SetDeploymentLogPath stores where the build output was written.
func (r *DeploymentRepo) SetDeploymentLogPath(ctx context.Context, id, path string) error {
	res, err := r.db.ExecContext(ctx, `UPDATE deployments SET log_path = ? WHERE id = ?`, path, id)
	if err != nil {
		return err
	}
	return rowsAffected(res)
}

// LogPathsForApplication lists the stored build logs of an application so the
// caller can remove the files before the rows cascade away.
func (r *DeploymentRepo) LogPathsForApplication(ctx context.Context, appID string) ([]string, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT log_path FROM deployments WHERE application_id = ? AND log_path != ''`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		out = append(out, path)
	}
	return out, rows.Err()
}

// PruneDeploymentsBefore removes deployment history older than cutoff and
// returns the log files that belonged to it, so the caller can unlink them
// after the rows are gone.
func (r *DeploymentRepo) PruneDeploymentsBefore(ctx context.Context, cutoff time.Time) ([]string, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT log_path FROM deployments WHERE queued_at < ? AND log_path != ''`, Text(cutoff))
	if err != nil {
		return nil, err
	}
	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			rows.Close()
			return nil, err
		}
		paths = append(paths, path)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if _, err := r.db.ExecContext(ctx, `DELETE FROM deployments WHERE queued_at < ?`, Text(cutoff)); err != nil {
		return nil, err
	}
	return paths, nil
}

// DeleteDeploymentsForApplication clears history when an app is removed.
func (r *DeploymentRepo) DeleteDeploymentsForApplication(ctx context.Context, appID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM deployments WHERE application_id = ?`, appID)
	return err
}
