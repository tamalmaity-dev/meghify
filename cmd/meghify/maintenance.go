package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"time"

	"github.com/tamalmaity-dev/meghify/internal/db"
	"github.com/tamalmaity-dev/meghify/internal/docker"
)

// jsonUnmarshal parses a job payload.
func jsonUnmarshal(payload string, dst any) error { return json.Unmarshal([]byte(payload), dst) }

// collectMetrics samples the machine the panel runs on and stores the result.
//
// Sampling reads the host directly (CPU, memory, disk, network), not the
// Docker engine, so metrics keep flowing when the engine is down. The row is
// written against the "local" node id so the dashboard can render one series
// even when no remote nodes exist yet; the matching nodes row is created and
// kept fresh on every collection.
func collectMetrics(ctx context.Context, store *db.Store) error {
	sample, ok := hostSampleNow()
	if !ok {
		return nil
	}

	// Metric rows are foreign-keyed to nodes, so the local row must exist
	// before the first sample on a fresh install.
	if err := store.Nodes.EnsureLocalNode(ctx); err != nil {
		return err
	}
	if err := store.Nodes.UpdateLocalNodeInfo(ctx, sample.OS, sample.Kernel, sample.CPUs, sample.MemTotal); err != nil {
		return err
	}

	return store.Nodes.InsertMetric(ctx, &db.NodeMetrics{
		ID:         db.NewID(),
		NodeID:     "local",
		CPUPercent: math.Round(sample.CPUPercent*10) / 10,
		MemUsed:    sample.MemUsed,
		MemTotal:   sample.MemTotal,
		DiskUsed:   sample.DiskUsed,
		DiskTotal:  sample.DiskTotal,
		NetRx:      sample.NetRx,
		NetTx:      sample.NetTx,
		RecordedAt: time.Now().UTC(),
	})
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
	// Host-metrics sampling no longer enters the queue, but panels upgraded from
	// older releases still hold thousands of rows: clear them within the hour.
	if _, err := store.Jobs.PruneJobsByKind(ctx, db.JobNodeMetrics, time.Now().Add(-time.Hour)); err != nil {
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
