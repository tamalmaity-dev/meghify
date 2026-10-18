package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"time"

	"github.com/tamalmaity-dev/meghify/internal/config"
	"github.com/tamalmaity-dev/meghify/internal/db"
	"github.com/tamalmaity-dev/meghify/internal/deploy"
	"github.com/tamalmaity-dev/meghify/internal/docker"
)

// jsonUnmarshal parses a job payload.
func jsonUnmarshal(payload string, dst any) error { return json.Unmarshal([]byte(payload), dst) }

// valueOr returns the first non-empty string.
func valueOr(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

// collectMetrics samples the machine the panel runs on and stores the result.
//
// Sampling reads the host directly (CPU, memory, disk, network), not the
// Docker engine, so metrics keep flowing when the engine is down. The row is
// written against the "local" node id so the dashboard can render one series
// even when no remote nodes exist yet; the matching nodes row is created and
// kept fresh on every collection.
//
// Hardware facts prefer the engine's report (the engine sees the real host,
// while /proc and /etc/os-release are namespaced into the panel container);
// the host sample is the fallback when no engine answers.
func collectMetrics(ctx context.Context, store *db.Store, client *docker.Client) error {
	sample, ok := hostSampleNow()
	if !ok {
		return nil
	}

	// Metric rows are foreign-keyed to nodes, so the local row must exist
	// before the first sample on a fresh install.
	if err := store.Nodes.EnsureLocalNode(ctx); err != nil {
		return err
	}

	osName, kernel, arch := sample.OS, sample.Kernel, sample.Arch
	cpus, memory := sample.CPUs, sample.MemTotal
	dockerVersion := ""
	if client != nil {
		if info, err := client.Info(ctx); err == nil {
			osName = valueOr(info.OperatingSystem, osName)
			kernel = valueOr(info.KernelVersion, kernel)
			arch = valueOr(info.Architecture, arch)
			if info.NCPU > 0 {
				cpus = info.NCPU
			}
			if info.MemTotal > 0 {
				memory = info.MemTotal
			}
			dockerVersion = info.ServerVersion
		}
	}

	var bootTime *time.Time
	if !sample.BootTime.IsZero() {
		bootTime = &sample.BootTime
	}
	if err := store.Nodes.UpdateLocalNodeFacts(ctx, osName, kernel, arch, dockerVersion, cpus, memory, bootTime); err != nil {
		return err
	}

	return store.Nodes.InsertMetric(ctx, &db.NodeMetrics{
		ID:         db.NewID(),
		NodeID:     db.LocalNodeID,
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

// collectGarbage enforces the retention window and prunes unused artifacts:
// stored rows, then Docker storage — dangling images, application images past
// the retention count and a build cache held within its bound, on the engine
// and inside the build sandbox.
func collectGarbage(ctx context.Context, store *db.Store, client *docker.Client, cfg *config.Config, logger *slog.Logger) error {
	retentionDays := cfg.MetricsRetentionDays
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
	protect := map[string]struct{}{}
	if containers, err := client.ListContainers(ctx, "", ""); err == nil {
		for _, container := range containers {
			if container.Image != "" {
				protect[container.Image] = struct{}{}
			}
		}
	}
	result, err := deploy.CleanupEngine(ctx, client, protect, cfg.ImageKeep, cfg.BuildCacheKeepBytes(), cfg.EffectiveHelperImage())
	if (result.Reclaimed > 0 || result.ImagesRemoved > 0) && logger != nil {
		logger.Info("docker cleanup reclaimed storage",
			"bytes", result.Reclaimed, "images", result.ImagesRemoved, "sandbox", result.Sandbox)
	}
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
