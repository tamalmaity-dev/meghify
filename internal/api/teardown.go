package api

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tamalmaity-dev/meghify/internal/db"
	"github.com/tamalmaity-dev/meghify/internal/deploy"
)

// teardownOptions selects which external resources are destroyed when a
// resource is deleted. Container removal and database-row cleanup are always
// performed; the tick boxes in the delete dialog control data, networking,
// routing and image hygiene.
type teardownOptions struct {
	Volumes   bool
	Networks  bool
	Workspace bool
	Routes    bool
	Images    bool
	// Prune runs the global dangling-image/build-cache cleanup. It is separate
	// from Images so deleting a project does not repeat a fleet-wide prune once
	// per application.
	Prune bool
}

// teardownApplication removes everything the panel created for one application
// on its target node. It is shared by application deletion and by deleting an
// environment/project, which used to remove only the database rows and leave
// containers, networks, volumes and build logs running forever.
func (s *Server) teardownApplication(ctx context.Context, app *db.Application, opts teardownOptions) map[string]any {
	result := map[string]any{
		"volumesRemoved":  0,
		"networkRemoved":  false,
		"configRemoved":   false,
		"spaceReclaimed":  int64(0),
		"containersGone":  false,
		"imagesRemoved":   int64(0),
		"deploymentsGone": false,
	}
	if app == nil {
		return result
	}

	if opts.Routes && s.pipeline != nil {
		if err := s.pipeline.ClearApplicationRoutes(ctx, app); err != nil {
			s.logger.Warn("route clear failed", "application", app.ID, "error", err)
		}
	}

	// Compose stacks create their own containers, so bring them down before
	// looking for panel-labelled application containers.
	if app.BuildPack == db.BuildCompose && s.pipeline != nil {
		composeCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		if err := s.pipeline.RemoveComposeApplication(composeCtx, app); err != nil {
			s.logger.Warn("could not remove compose application stack", "application", app.ID, "error", err)
		}
		cancel()
	}

	client, clientErr := s.clientForNode(ctx, app.NodeID)
	if clientErr != nil {
		s.logger.Warn("could not reach node while deleting application; engine resources may remain",
			"application", app.ID, "node", app.NodeID, "error", clientErr)
		client = nil
	}

	if client != nil {
		// Label-based lookup catches both the final container and a rolling
		// release left behind under a temporary name.
		containers, err := client.ListContainers(ctx, "meghify.application", app.ID)
		if err != nil {
			s.logger.Warn("could not list application containers", "application", app.ID, "error", err)
		}
		for _, item := range containers {
			_ = client.StopContainer(ctx, item.ID, 20)
			_ = client.RemoveContainer(ctx, item.ID)
			result["containersGone"] = true
		}

		volumes, err := s.store.Applications.VolumesByTarget(ctx, db.TargetApplication, app.ID)
		if err != nil {
			s.logger.Warn("could not list application volumes", "application", app.ID, "error", err)
		}
		removed := 0
		for _, volume := range volumes {
			if opts.Volumes {
				if err := client.RemoveVolume(ctx, deploy.VolumeName(volume.ID)); err != nil {
					s.logger.Warn("could not remove named volume", "volume", volume.ID, "error", err)
				} else {
					removed++
				}
			}
			_ = s.store.Applications.DeleteVolume(ctx, volume.ID)
		}
		result["volumesRemoved"] = removed

		if opts.Networks {
			if err := client.RemoveNetwork(ctx, deploy.NetworkName(app)); err == nil {
				result["networkRemoved"] = true
			}
		}

		if opts.Images {
			images, err := client.ListImages(ctx)
			if err == nil {
				count := int64(0)
				for _, summary := range images {
					if summary.Labels["meghify.application"] != app.ID {
						continue
					}
					if err := client.RemoveImage(ctx, summary.ID); err == nil {
						count++
					}
				}
				result["imagesRemoved"] = count
			}
		}
		if opts.Prune {
			var reclaimed int64
			if bytes, err := client.PruneImages(ctx); err == nil {
				reclaimed += bytes
			}
			if bytes, err := client.BuildCachePrune(ctx); err == nil {
				reclaimed += bytes
			}
			result["spaceReclaimed"] = reclaimed
		}
	} else {
		// Still drop the volume records so a later re-create does not inherit
		// stale mounts. The named volumes themselves are preserved if the
		// engine is unreachable.
		if volumes, err := s.store.Applications.VolumesByTarget(ctx, db.TargetApplication, app.ID); err == nil {
			for _, volume := range volumes {
				_ = s.store.Applications.DeleteVolume(ctx, volume.ID)
			}
		}
	}

	if opts.Workspace {
		// The workspace is wholly derived from the record ID; JoinRelative is a
		// second guard against a malformed ID turning into a root-level delete.
		workspace, err := deploy.JoinRelative(filepath.Join(s.cfg.DataDir, "workspaces"), app.ID)
		if err == nil {
			if err := os.RemoveAll(workspace); err == nil {
				result["configRemoved"] = true
			}
		} else {
			s.logger.Warn("refusing to remove unsafe workspace path", "application", app.ID, "error", err)
		}
	}

	// Build logs belong to the application history. Remove only files inside
	// the panel's deployments directory, never an arbitrary path from a row.
	logPaths, err := s.store.Deployments.LogPathsForApplication(ctx, app.ID)
	if err == nil {
		logRoot := filepath.Join(s.cfg.DataDir, "deployments")
		for _, path := range logPaths {
			if safePathUnder(logRoot, path) {
				_ = os.Remove(path)
			}
		}
	}
	if err := s.store.Deployments.DeleteDeploymentsForApplication(ctx, app.ID); err != nil {
		s.logger.Warn("could not clear deployment history", "application", app.ID, "error", err)
	} else {
		result["deploymentsGone"] = true
	}
	return result
}

// teardownDatabase removes a database container from its target node and, when
// requested, the named data volume behind it. Backup rows are removed too; no
// backup artifact is uploaded in this version, so there is no file to unlink.
func (s *Server) teardownDatabase(ctx context.Context, database *db.Database, removeData bool) {
	if database == nil {
		return
	}
	client, err := s.clientForNode(ctx, database.NodeID)
	if err != nil {
		s.logger.Warn("could not reach node while deleting database",
			"database", database.ID, "node", database.NodeID, "error", err)
	} else {
		containers, listErr := client.ListContainers(ctx, "meghify.database", database.ID)
		if listErr != nil {
			s.logger.Warn("could not list database containers", "database", database.ID, "error", listErr)
		}
		for _, container := range containers {
			_ = client.StopContainer(ctx, container.ID, 20)
			_ = client.RemoveContainer(ctx, container.ID)
		}
		if removeData {
			if err := client.RemoveVolume(ctx, databaseVolumeName(database)); err != nil {
				s.logger.Warn("could not remove database volume", "database", database.ID, "error", err)
			}
		}
	}
	if backups, err := s.store.Resources.BackupsByTarget(ctx, db.TargetDatabase, database.ID); err == nil {
		for _, backup := range backups {
			_ = s.store.Resources.DeleteBackup(ctx, backup.ID)
		}
	}
}

// removeServiceFiles drops the panel-side compose file and stdout log after a
// service row is gone. Both live under the services directory; neither belongs
// to a node, so they are safe to remove even when the remote stack is gone.
func (s *Server) removeServiceFiles(id string) {
	root := filepath.Join(s.cfg.DataDir, "services")
	if dir, err := deploy.JoinRelative(root, id); err == nil {
		_ = os.RemoveAll(dir)
	}
	logPath := filepath.Join(root, id+".log")
	if safePathUnder(root, logPath) {
		_ = os.Remove(logPath)
	}
}

// safePathUnder reports whether path is a direct descendant of root. Both are
// cleaned first so a stored "../" cannot escape.
func safePathUnder(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
