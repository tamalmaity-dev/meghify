package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Docker cleanup trigger, image-retention and run-state values shared by the
// API, the scheduler and the server page.
const (
	CleanupTriggerSchedule  = "schedule"
	CleanupTriggerThreshold = "threshold"

	CleanupImagesKeep   = "keep"
	CleanupImagesRemove = "remove"

	CleanupRunRunning = "running"
	CleanupRunSuccess = "success"
	CleanupRunFailed  = "failed"
)

// DockerCleanupSettings is the single server-scoped cleanup configuration.
type DockerCleanupSettings struct {
	Enabled           bool      `json:"enabled"`
	Schedule          string    `json:"schedule"`
	Trigger           string    `json:"trigger"`
	ThresholdPercent  int       `json:"thresholdPercent"`
	UnusedVolumes     bool      `json:"unusedVolumes"`
	UnusedNetworks    bool      `json:"unusedNetworks"`
	ApplicationImages string    `json:"applicationImages"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

// DefaultDockerCleanupSettings is used until the operator saves a schedule.
func DefaultDockerCleanupSettings() DockerCleanupSettings {
	return DockerCleanupSettings{
		Enabled:           true,
		Schedule:          "0 0 * * *",
		Trigger:           CleanupTriggerSchedule,
		ThresholdPercent:  80,
		UnusedVolumes:     false,
		UnusedNetworks:    false,
		ApplicationImages: CleanupImagesKeep,
	}
}

// DockerCleanupRun is one execution shown in the recent-executions list.
type DockerCleanupRun struct {
	ID                 string    `json:"id"`
	Trigger            string    `json:"trigger"`
	Status             string    `json:"status"`
	StartedAt          time.Time `json:"startedAt"`
	FinishedAt         time.Time `json:"finishedAt,omitempty"`
	DurationMS         int64     `json:"durationMs"`
	ReclaimedBytes     int64     `json:"reclaimedBytes"`
	ImagesRemoved      int64     `json:"imagesRemoved"`
	OrphanedContainers int64     `json:"orphanedContainers"`
	VolumesRemoved     int64     `json:"volumesRemoved"`
	NetworksRemoved    int64     `json:"networksRemoved"`
	Error              string    `json:"error,omitempty"`
}

// DockerCleanupRepo persists the cleanup schedule and its execution history.
type DockerCleanupRepo struct{ db *sql.DB }

// Settings returns the singleton configuration, creating defaults on first use.
func (r *DockerCleanupRepo) Settings(ctx context.Context) (*DockerCleanupSettings, error) {
	now := Text(time.Now().UTC())
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO docker_cleanup_settings (id, updated_at) VALUES ('local', ?)
		 ON CONFLICT (id) DO NOTHING`, now); err != nil {
		return nil, fmt.Errorf("ensure docker cleanup settings: %w", err)
	}

	var s DockerCleanupSettings
	var updated string
	err := r.db.QueryRowContext(ctx,
		`SELECT enabled, schedule, trigger, threshold_percent, unused_volumes,
		        unused_networks, application_images, updated_at
		   FROM docker_cleanup_settings WHERE id = 'local'`).
		Scan(&s.Enabled, &s.Schedule, &s.Trigger, &s.ThresholdPercent, &s.UnusedVolumes,
			&s.UnusedNetworks, &s.ApplicationImages, &updated)
	if err != nil {
		return nil, fmt.Errorf("read docker cleanup settings: %w", err)
	}
	s.UpdatedAt = Parse(updated)
	return &s, nil
}

// SaveSettings writes the singleton configuration.
func (r *DockerCleanupRepo) SaveSettings(ctx context.Context, s *DockerCleanupSettings) error {
	if s == nil {
		return errors.New("docker cleanup settings are required")
	}
	now := Text(time.Now().UTC())
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO docker_cleanup_settings (id, updated_at) VALUES ('local', ?)
		 ON CONFLICT (id) DO NOTHING`, now); err != nil {
		return fmt.Errorf("ensure docker cleanup settings: %w", err)
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE docker_cleanup_settings
		    SET enabled = ?, schedule = ?, trigger = ?, threshold_percent = ?,
		        unused_volumes = ?, unused_networks = ?, application_images = ?, updated_at = ?
		  WHERE id = 'local'`,
		s.Enabled, s.Schedule, s.Trigger, s.ThresholdPercent, s.UnusedVolumes,
		s.UnusedNetworks, s.ApplicationImages, now)
	if err != nil {
		return fmt.Errorf("save docker cleanup settings: %w", err)
	}
	if err := rowsAffected(res); err != nil {
		return err
	}
	s.UpdatedAt = Parse(now)
	return nil
}

// AddRun records the start of an execution.
func (r *DockerCleanupRepo) AddRun(ctx context.Context, run *DockerCleanupRun) error {
	if run == nil {
		return errors.New("docker cleanup run is required")
	}
	var finished any
	if !run.FinishedAt.IsZero() {
		finished = Text(run.FinishedAt)
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO docker_cleanup_runs
		    (id, trigger, status, started_at, finished_at, duration_ms, reclaimed_bytes,
		     images_removed, orphaned_containers, volumes_removed, networks_removed, error)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.Trigger, run.Status, Text(run.StartedAt), finished, run.DurationMS,
		run.ReclaimedBytes, run.ImagesRemoved, run.OrphanedContainers,
		run.VolumesRemoved, run.NetworksRemoved, run.Error)
	if err != nil {
		return fmt.Errorf("insert docker cleanup run: %w", err)
	}
	return nil
}

// FinishRun marks an execution complete and stores its result.
func (r *DockerCleanupRepo) FinishRun(ctx context.Context, id, status string, duration time.Duration, reclaimed, images, orphaned, volumes, networks int64, errText string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE docker_cleanup_runs
		    SET status = ?, finished_at = ?, duration_ms = ?, reclaimed_bytes = ?,
		        images_removed = ?, orphaned_containers = ?, volumes_removed = ?,
		        networks_removed = ?, error = ?
		  WHERE id = ?`,
		status, Text(time.Now().UTC()), duration.Milliseconds(), reclaimed, images,
		orphaned, volumes, networks, errText, id)
	if err != nil {
		return fmt.Errorf("finish docker cleanup run: %w", err)
	}
	return nil
}

// Runs returns recent executions, newest first.
func (r *DockerCleanupRepo) Runs(ctx context.Context, limit int) ([]*DockerCleanupRun, error) {
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, trigger, status, started_at, finished_at, duration_ms,
		        reclaimed_bytes, images_removed, orphaned_containers,
		        volumes_removed, networks_removed, error
		   FROM docker_cleanup_runs ORDER BY started_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list docker cleanup runs: %w", err)
	}
	defer rows.Close()

	out := make([]*DockerCleanupRun, 0, limit)
	for rows.Next() {
		var run DockerCleanupRun
		var started string
		var finished sql.NullString
		if err := rows.Scan(&run.ID, &run.Trigger, &run.Status, &started, &finished,
			&run.DurationMS, &run.ReclaimedBytes, &run.ImagesRemoved,
			&run.OrphanedContainers, &run.VolumesRemoved, &run.NetworksRemoved,
			&run.Error); err != nil {
			return nil, err
		}
		run.StartedAt = Parse(started)
		run.FinishedAt = Parse(nullString(finished))
		out = append(out, &run)
	}
	return out, rows.Err()
}

// FailRunningRuns closes executions left behind by an unclean panel exit. A
// stale "running" row would otherwise keep the scheduler from ever considering
// the next cron occurrence.
func (r *DockerCleanupRepo) FailRunningRuns(ctx context.Context, reason string) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE docker_cleanup_runs
		    SET status = ?, finished_at = ?, error = ?
		  WHERE status = ?`,
		CleanupRunFailed, Text(time.Now().UTC()), reason, CleanupRunRunning)
	if err != nil {
		return 0, fmt.Errorf("fail stale docker cleanup runs: %w", err)
	}
	return res.RowsAffected()
}

// PruneRuns keeps the execution table bounded. Running rows are never removed.
func (r *DockerCleanupRepo) PruneRuns(ctx context.Context, before time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM docker_cleanup_runs WHERE started_at < ? AND status != ?`,
		Text(before), CleanupRunRunning)
	if err != nil {
		return 0, fmt.Errorf("prune docker cleanup runs: %w", err)
	}
	return res.RowsAffected()
}

// LatestScheduledRun returns the newest scheduled execution, including one
// still running. It is used to decide whether the next cron occurrence is due.
func (r *DockerCleanupRepo) LatestScheduledRun(ctx context.Context) (*DockerCleanupRun, error) {
	var run DockerCleanupRun
	var started string
	var finished sql.NullString
	err := r.db.QueryRowContext(ctx,
		`SELECT id, trigger, status, started_at, finished_at, duration_ms,
		        reclaimed_bytes, images_removed, orphaned_containers,
		        volumes_removed, networks_removed, error
		   FROM docker_cleanup_runs WHERE trigger = ? ORDER BY started_at DESC LIMIT 1`,
		CleanupTriggerSchedule).
		Scan(&run.ID, &run.Trigger, &run.Status, &started, &finished, &run.DurationMS,
			&run.ReclaimedBytes, &run.ImagesRemoved, &run.OrphanedContainers,
			&run.VolumesRemoved, &run.NetworksRemoved, &run.Error)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("latest docker cleanup run: %w", err)
	}
	run.StartedAt = Parse(started)
	run.FinishedAt = Parse(nullString(finished))
	return &run, nil
}
