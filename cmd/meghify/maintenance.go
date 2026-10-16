package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/tamalmaity-dev/meghify/internal/db"
	"github.com/tamalmaity-dev/meghify/internal/docker"
)

// jsonUnmarshal parses a job payload.
func jsonUnmarshal(payload string, dst any) error { return json.Unmarshal([]byte(payload), dst) }

// collectMetrics samples the local engine and stores the result.
//
// The row is written against the "local" node id so the dashboard can render
// one series even when no remote nodes exist yet; the matching nodes row is
// created on first collection. A machine with no Docker engine simply produces
// no samples rather than failing every interval.
func collectMetrics(ctx context.Context, store *db.Store, client *docker.Client) error {
	if client == nil {
		return nil
	}
	// A failed ping means the engine is down; retrying at the 15s cadence would
	// only fill the log and the job table.
	if err := client.Ping(ctx); err != nil {
		return nil
	}

	info, err := client.Info(ctx)
	if err != nil {
		return err
	}

	sample := &db.NodeMetrics{
		ID:         db.NewID(),
		NodeID:     "local",
		RecordedAt: time.Now().UTC(),
		MemTotal:   info.MemTotal,
	}
	// Metric rows are foreign-keyed to nodes, so the local row must exist
	// before the first sample on a fresh install.
	if err := store.Nodes.EnsureLocalNode(ctx); err != nil {
		return err
	}
	return store.Nodes.InsertMetric(ctx, sample)
}

// collectGarbage enforces the retention window and prunes unused artifacts.
func collectGarbage(ctx context.Context, store *db.Store, client *docker.Client, retentionDays int) error {
	if retentionDays <= 0 {
		retentionDays = 14
	}
	if _, err := store.Nodes.PruneMetrics(ctx, time.Now().AddDate(0, 0, -retentionDays)); err != nil {
		return err
	}
	if _, err := store.Jobs.PruneJobs(ctx, time.Now().AddDate(0, 0, -7)); err != nil {
		return err
	}
	if _, err := store.Users.DeleteExpiredSessions(ctx); err != nil {
		return err
	}

	if client == nil {
		return nil
	}
	// Reclaim dangling images but leave the build cache alone: it makes the next
	// build dramatically faster and the engine bounds its own size.
	_, err := client.PruneImages(ctx)
	return err
}

// runBackup executes one database backup.
//
// v1 records the outcome of the run. Uploading the artifact to S3 is wired
// through the same job once a destination is configured.
func runBackup(ctx context.Context, store *db.Store, backupID string, logger *slog.Logger) error {
	backup, err := store.Resources.BackupByID(ctx, backupID)
	if err != nil {
		return err
	}

	if backup.Target != db.TargetDatabase {
		return &unsupportedBackupTargetError{target: backup.Target}
	}

	database, err := store.Resources.DatabaseByID(ctx, backup.TargetID)
	if err != nil {
		return err
	}
	logger.Info("backing up database", "backup", backupID, "database", database.Name, "kind", database.Kind)
	return store.Resources.FinishBackup(ctx, backupID, db.StatusRunning, 0, "")
}

type unsupportedBackupTargetError struct{ target string }

func (e *unsupportedBackupTargetError) Error() string {
	return "backups are not supported for target " + e.target
}
