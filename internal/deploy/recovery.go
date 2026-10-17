package deploy

import (
	"context"
	"log/slog"
	"time"

	"github.com/tamalmaity-dev/meghify/internal/db"
	"github.com/tamalmaity-dev/meghify/internal/docker"
)

// RecoverInterrupted reconciles state after an unclean panel restart. Dokploy
// does the same: a deployment that was running when the panel died is
// canceled, never left "running" forever — a stuck row would block every
// later deploy of that application. Orphaned build helpers and rolling-release
// leftovers are removed too.
func RecoverInterrupted(ctx context.Context, store *db.Store, client *docker.Client, logger *slog.Logger) {
	appIDs, err := store.Deployments.CancelInFlight(ctx, "interrupted by a panel restart")
	if err != nil {
		logger.Error("could not reconcile interrupted deployments", "error", err)
		return
	}

	for _, id := range appIDs {
		app, err := store.Applications.ApplicationByID(ctx, id)
		if err != nil {
			continue
		}
		status := db.StatusError
		if client != nil {
			if container, err := client.ContainerByName(ctx, app.Name); err == nil && container != nil {
				if container.State == "running" {
					status = db.StatusRunning
				} else {
					status = db.StatusStopped
				}
			}
			cleanupTempContainers(ctx, client, app)
		}
		if err := store.Applications.PatchApplicationStatus(ctx, id, status); err != nil {
			logger.Warn("could not restore application status", "application", app.Name, "error", err)
		}
		logger.Warn("deployment interrupted by restart, marked canceled",
			"application", app.Name, "status", status)
	}

	if client == nil {
		return
	}
	helpers, err := client.ListContainers(ctx, "meghify.role", "build-helper")
	if err != nil {
		logger.Warn("could not list orphaned build helpers", "error", err)
		return
	}
	for _, helper := range helpers {
		_ = client.StopContainer(ctx, helper.ID, 5*time.Second)
		if err := client.RemoveContainer(ctx, helper.ID); err != nil {
			logger.Warn("could not remove orphaned build helper", "container", helper.Name, "error", err)
			continue
		}
		logger.Info("removed orphaned build helper", "container", helper.Name)
	}
}

// cleanupTempContainers removes rolling-release leftovers of an application:
// containers labeled for it that do not carry its final name. They only exist
// when a deployment died between create and rename.
func cleanupTempContainers(ctx context.Context, client *docker.Client, app *db.Application) {
	finalName := docker.ManagedName(app.Name)
	list, err := client.ListContainers(ctx, "meghify.application", app.ID)
	if err != nil {
		return
	}
	for _, item := range list {
		if item.Name == finalName {
			continue
		}
		_ = client.StopContainer(ctx, item.ID, 5*time.Second)
		_ = client.RemoveContainer(ctx, item.ID)
	}
}
