package api

// Docker storage: a breakdown of what the engine keeps (the dashboard's
// "Docker disk usage" card) and a manual cleanup that reclaims the same
// categories the hourly GC pass targets.

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/tamalmaity-dev/meghify/internal/deploy"
	"github.com/tamalmaity-dev/meghify/internal/docker"
)

const (
	// Docker's /system/df call walks image layers, volumes and build cache.
	// On a small VPS this can consume a visible slice of a CPU core, so the
	// dashboard reuses one measurement instead of rescanning on every open.
	dockerUsageCacheTTL = 15 * time.Minute
	dockerUsageTimeout  = 2 * time.Minute
)

type dockerDiskUsagePayload struct {
	Engine    docker.DiskUsageReport `json:"engine"`
	Top       []docker.UsageItem     `json:"top"`
	Sandbox   *deploy.SandboxUsage   `json:"sandbox,omitempty"`
	SampledAt time.Time              `json:"sampledAt"`
}

// dockerDiskUsage answers the dashboard's Docker disk usage card. Normal
// requests can be served from a recent measurement; ?fresh=1 is used by the
// explicit Refresh button and always blocks for a new scan.
func (s *Server) dockerDiskUsage(w http.ResponseWriter, r *http.Request) {
	if s.docker == nil {
		writeError(w, http.StatusServiceUnavailable, "the Docker engine is unavailable")
		return
	}

	force := r.URL.Query().Get("fresh") == "1"
	if !force {
		s.dockerUsageMu.Lock()
		cached, sampledAt := s.dockerUsage, s.dockerUsageAt
		s.dockerUsageMu.Unlock()
		if cached != nil && dockerUsageCacheFresh(sampledAt, time.Now()) {
			writeJSON(w, http.StatusOK, cached)
			return
		}
	}

	payload, err := s.loadDockerUsage(r.Context())
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func dockerUsageCacheFresh(sampledAt, now time.Time) bool {
	return !sampledAt.IsZero() && now.Sub(sampledAt) < dockerUsageCacheTTL
}

// loadDockerUsage serialises expensive scans. Two browser tabs opening the
// dashboard at once must not both walk the engine's stores.
func (s *Server) loadDockerUsage(ctx context.Context) (*dockerDiskUsagePayload, error) {
	started := time.Now()
	s.dockerUsageLoadMu.Lock()
	defer s.dockerUsageLoadMu.Unlock()

	s.dockerUsageMu.Lock()
	if cached := s.dockerUsage; cached != nil && !s.dockerUsageAt.Before(started) {
		s.dockerUsageMu.Unlock()
		return cached, nil
	}
	s.dockerUsageMu.Unlock()

	payload, err := s.collectDockerUsage(ctx)
	if err != nil {
		return nil, err
	}
	s.dockerUsageMu.Lock()
	s.dockerUsage = payload
	s.dockerUsageAt = payload.SampledAt
	s.dockerUsageMu.Unlock()
	return payload, nil
}

func (s *Server) collectDockerUsage(ctx context.Context) (*dockerDiskUsagePayload, error) {
	ctx, cancel := context.WithTimeout(ctx, dockerUsageTimeout)
	defer cancel()

	engine, top, err := s.docker.DiskUsageDetail(ctx)
	if err != nil {
		return nil, err
	}
	payload := &dockerDiskUsagePayload{
		Engine:    engine,
		Top:       top,
		SampledAt: time.Now().UTC(),
	}
	// The sandbox decomposition is best-effort: a stopped or missing sandbox
	// simply renders without its row.
	if usage, ok, err := deploy.SandboxDiskUsage(ctx, s.docker); err == nil && ok {
		payload.Sandbox = &usage
	}
	return payload, nil
}

func (s *Server) clearDockerUsageCache() {
	s.dockerUsageMu.Lock()
	s.dockerUsage = nil
	s.dockerUsageAt = time.Time{}
	s.dockerUsageMu.Unlock()
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

	result, err := deploy.CleanupEngine(r.Context(), s.docker, protect, s.cfg.ImageKeep, s.cfg.BuildCacheKeepBytes(), s.cfg.EffectiveHelperImage(), s.cfg.ProxyImage)
	s.clearDockerUsageCache()
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
