package deploy

// Configured Docker cleanup is the user-facing side of CleanupEngine. The
// server page stores a schedule and opt-in resource options in SQLite, and
// both the scheduler and the manual "Run cleanup" button execute through this
// one function so history and audit records cannot drift apart.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tamalmaity-dev/meghify/internal/config"
	"github.com/tamalmaity-dev/meghify/internal/db"
	"github.com/tamalmaity-dev/meghify/internal/docker"
)

// RunDockerCleanup executes one configured pass and records it in the
// docker_cleanup_runs history. trigger is "manual" or "schedule". applyOptions
// enables the destructive advanced options saved on the server page; callers
// such as the dashboard's generic "Free up space" button leave it false.
func RunDockerCleanup(ctx context.Context, client *docker.Client, store *db.Store, cfg *config.Config, trigger string, applyOptions bool) (CleanupResult, error) {
	var result CleanupResult
	if store == nil || cfg == nil {
		return result, errors.New("docker cleanup is not configured")
	}

	settings, err := store.DockerCleanup.Settings(ctx)
	if err != nil {
		return result, err
	}

	started := time.Now().UTC()
	run := &db.DockerCleanupRun{
		ID:        db.NewID(),
		Trigger:   trigger,
		Status:    db.CleanupRunRunning,
		StartedAt: started,
	}
	if err := store.DockerCleanup.AddRun(ctx, run); err != nil {
		return result, err
	}

	finish := func(cleanupErr error) error {
		status := db.CleanupRunSuccess
		errText := ""
		if cleanupErr != nil {
			status = db.CleanupRunFailed
			errText = cleanupErr.Error()
		}
		// The job context may have been canceled by the time the cleanup
		// returns; the history row still needs to leave "running" so the UI
		// can render it.
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		finishErr := store.DockerCleanup.FinishRun(finishCtx, run.ID, status,
			time.Since(started), result.Reclaimed, result.ImagesRemoved,
			result.OrphanedContainers, result.VolumesRemoved, result.NetworksRemoved,
			truncateError(errText))
		return errors.Join(cleanupErr, finishErr)
	}

	if client == nil {
		return result, finish(docker.ErrEngineUnavailable)
	}

	// Images served by any container, running or stopped, survive the pass.
	protect := map[string]struct{}{}
	if containers, listErr := client.ListContainers(ctx, "", ""); listErr == nil {
		for _, container := range containers {
			if container.Image != "" {
				protect[container.Image] = struct{}{}
			}
		}
	} else {
		return result, finish(fmt.Errorf("list containers for cleanup: %w", listErr))
	}

	knownApps := map[string]struct{}{}
	if apps, listErr := store.Applications.ListApplications(ctx); listErr != nil {
		// A database hiccup must not turn every application container into an
		// orphan. Skip orphan pruning and let the next run retry.
		knownApps = nil
	} else {
		for _, app := range apps {
			if app != nil {
				knownApps[app.ID] = struct{}{}
			}
		}
	}

	imageKeep := cfg.ImageKeep
	unusedVolumes := false
	unusedNetworks := false
	if applyOptions {
		if settings.ApplicationImages == db.CleanupImagesRemove {
			// "Remove retained images" keeps only what the current containers
			// reference; the configured rollback window is bypassed for this
			// pass.
			imageKeep = 1
		}
		unusedVolumes = settings.UnusedVolumes
		unusedNetworks = settings.UnusedNetworks
	}

	result, cleanupErr := CleanupEngine(ctx, client, CleanupOptions{
		Protect:        protect,
		KnownApps:      knownApps,
		ImageKeep:      imageKeep,
		CacheKeep:      cfg.BuildCacheKeepBytes(),
		HelperImage:    cfg.EffectiveHelperImage(),
		ProxyImage:     cfg.ProxyImage,
		UnusedVolumes:  unusedVolumes,
		UnusedNetworks: unusedNetworks,
	})
	return result, finish(cleanupErr)
}

func truncateError(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 2000 {
		return value
	}
	return value[:2000]
}
