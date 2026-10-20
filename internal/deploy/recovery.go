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
func RecoverInterrupted(
	ctx context.Context,
	store *db.Store,
	resolve func(ctx context.Context, nodeID string) (*docker.Client, error),
	logger *slog.Logger,
) {
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
		if resolve != nil {
			if client, err := resolve(ctx, app.NodeID); err == nil && client != nil {
				if container, err := client.ContainerByName(ctx, ContainerName(app)); err == nil && container != nil {
					if container.State == "running" {
						status = db.StatusRunning
					} else {
						status = db.StatusStopped
					}
				}
				cleanupTempContainers(ctx, client, app)
			}
		}
		if err := store.Applications.PatchApplicationStatus(ctx, id, status); err != nil {
			logger.Warn("could not restore application status", "application", app.Name, "error", err)
		}
		logger.Warn("deployment interrupted by restart, marked canceled",
			"application", app.Name, "status", status)
	}

	if resolve == nil {
		return
	}
	// Orphaned helpers can exist on the local engine and on every remote node.
	// Query all of them; an unreachable node is skipped, not fatal.
	nodes, err := store.Nodes.ListNodes(ctx)
	if err != nil {
		logger.Warn("could not list nodes for helper recovery", "error", err)
		return
	}
	seen := map[string]bool{"local": true}
	for _, n := range nodes {
		if n.ID == "" || seen[n.ID] {
			continue
		}
		seen[n.ID] = true
		client, err := resolve(ctx, n.ID)
		if err != nil || client == nil {
			continue
		}
		helpers, err := client.ListContainers(ctx, "meghify.role", "build-helper")
		if err != nil {
			logger.Warn("could not list orphaned build helpers", "node", n.ID, "error", err)
			continue
		}
		for _, helper := range helpers {
			_ = client.StopContainer(ctx, helper.ID, 5*time.Second)
			if err := client.RemoveContainer(ctx, helper.ID); err != nil {
				logger.Warn("could not remove orphaned build helper", "node", n.ID, "container", helper.Name, "error", err)
				continue
			}
			logger.Info("removed orphaned build helper", "node", n.ID, "container", helper.Name)
		}
	}
}

// cleanupTempContainers removes rolling-release leftovers of an application:
// containers labeled for it that do not carry its final name. They only exist
// when a deployment died between create and rename.
func cleanupTempContainers(ctx context.Context, client *docker.Client, app *db.Application) {
	finalName := docker.ManagedName(ContainerName(app))
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
