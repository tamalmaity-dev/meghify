package api

// Docker storage: a breakdown of what the engine keeps (the dashboard's
// "Docker disk usage" card) and a manual cleanup that reclaims the same
// categories the hourly GC pass targets.

import (
	"net/http"
	"strings"

	"github.com/tamalmaity-dev/meghify/internal/deploy"
	"github.com/tamalmaity-dev/meghify/internal/docker"
)

type dockerDiskUsagePayload struct {
	Engine  docker.DiskUsageReport `json:"engine"`
	Top     []docker.UsageItem     `json:"top"`
	Sandbox *deploy.SandboxUsage   `json:"sandbox,omitempty"`
}

// dockerDiskUsage answers the dashboard's Docker disk usage card.
func (s *Server) dockerDiskUsage(w http.ResponseWriter, r *http.Request) {
	if s.docker == nil {
		writeError(w, http.StatusServiceUnavailable, "the Docker engine is unavailable")
		return
	}
	engine, top, err := s.docker.DiskUsageDetail(r.Context())
	if err != nil {
		mapError(w, err)
		return
	}
	payload := dockerDiskUsagePayload{Engine: engine, Top: top}
	// The sandbox decomposition is best-effort: a stopped or missing sandbox
	// simply renders without its row.
	if usage, ok, err := deploy.SandboxDiskUsage(r.Context(), s.docker); err == nil && ok {
		payload.Sandbox = &usage
	}
	writeJSON(w, http.StatusOK, payload)
}

// dockerCleanup runs one cleanup pass and reports what it reclaimed.
func (s *Server) dockerCleanup(w http.ResponseWriter, r *http.Request) {
	if s.docker == nil {
		writeError(w, http.StatusServiceUnavailable, "the Docker engine is unavailable")
		return
	}

	// Images a running container serves from always survive the pass.
	protect := map[string]struct{}{}
	if containers, err := s.docker.ListContainers(r.Context(), "", ""); err == nil {
		for _, container := range containers {
			if container.Image != "" {
				protect[container.Image] = struct{}{}
			}
		}
	}

	result, err := deploy.CleanupEngine(r.Context(), s.docker, protect, s.cfg.ImageKeep, s.cfg.BuildCacheKeepBytes(), s.cfg.EffectiveHelperImage())
	if result.Reclaimed > 0 || result.ImagesRemoved > 0 {
		s.logger.Info("manual docker cleanup reclaimed storage",
			"bytes", result.Reclaimed, "images", result.ImagesRemoved, "sandbox", result.Sandbox)
	}
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		s.audit(r.Context(), r, "docker.cleanup", "docker", "", map[string]any{
			"reclaimed":     result.Reclaimed,
			"imagesRemoved": result.ImagesRemoved,
		})
		mapError(w, err)
		return
	}
	s.audit(r.Context(), r, "docker.cleanup", "docker", "", map[string]any{
		"reclaimed":     result.Reclaimed,
		"imagesRemoved": result.ImagesRemoved,
	})
	writeJSON(w, http.StatusOK, result)
}
