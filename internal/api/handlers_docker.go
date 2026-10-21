package api

// Docker storage: a breakdown of what the engine keeps (the dashboard's
// "Docker disk usage" card) and a manual cleanup that reclaims the same
// categories the hourly GC pass targets.

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/tamalmaity-dev/meghify/internal/db"
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

// dockerCleanupSettings returns the server-scoped Docker cleanup schedule.
func (s *Server) dockerCleanupSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.DockerCleanup.Settings(r.Context())
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

// updateDockerCleanupSettings validates and saves the cleanup schedule. The
// scheduler reads the same row, so a save takes effect on the next minute
// without restarting the panel.
func (s *Server) updateDockerCleanupSettings(w http.ResponseWriter, r *http.Request) {
	var body db.DockerCleanupSettings
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateDockerCleanupSettings(&body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.DockerCleanup.SaveSettings(r.Context(), &body); err != nil {
		mapError(w, err)
		return
	}
	s.audit(r.Context(), r, "docker.cleanup_settings", "docker", "", map[string]any{
		"enabled":  body.Enabled,
		"schedule": body.Schedule,
		"trigger":  body.Trigger,
	})
	writeJSON(w, http.StatusOK, body)
}

func validateDockerCleanupSettings(settings *db.DockerCleanupSettings) error {
	defaults := db.DefaultDockerCleanupSettings()
	settings.Schedule = strings.TrimSpace(settings.Schedule)
	settings.Trigger = strings.TrimSpace(settings.Trigger)
	settings.ApplicationImages = strings.TrimSpace(settings.ApplicationImages)
	if settings.Schedule == "" {
		settings.Schedule = defaults.Schedule
	}
	if settings.Trigger == "" {
		settings.Trigger = defaults.Trigger
	}
	if settings.ApplicationImages == "" {
		settings.ApplicationImages = defaults.ApplicationImages
	}
	if settings.ThresholdPercent == 0 {
		settings.ThresholdPercent = defaults.ThresholdPercent
	}
	if len(settings.Schedule) > 128 {
		return fmt.Errorf("schedule is too long")
	}
	if _, err := cron.ParseStandard(settings.Schedule); err != nil {
		return fmt.Errorf("schedule must be a valid cron expression: %w", err)
	}
	switch settings.Trigger {
	case db.CleanupTriggerSchedule, db.CleanupTriggerThreshold:
	default:
		return fmt.Errorf("trigger must be %q or %q", db.CleanupTriggerSchedule, db.CleanupTriggerThreshold)
	}
	if settings.ThresholdPercent < 1 || settings.ThresholdPercent > 99 {
		return fmt.Errorf("threshold must be between 1 and 99 percent")
	}
	switch settings.ApplicationImages {
	case db.CleanupImagesKeep, db.CleanupImagesRemove:
	default:
		return fmt.Errorf("application images must be %q or %q", db.CleanupImagesKeep, db.CleanupImagesRemove)
	}
	return nil
}

// dockerCleanupHistory powers the "Recent executions" list.
func (s *Server) dockerCleanupHistory(w http.ResponseWriter, r *http.Request) {
	limit := 10
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	runs, err := s.store.DockerCleanup.Runs(r.Context(), limit)
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

// dockerCleanup runs one cleanup pass with the saved options and reports what
// it reclaimed. The run is recorded in the same history the scheduler writes.
func (s *Server) dockerCleanup(w http.ResponseWriter, r *http.Request) {
	// The server page opts into its saved advanced options explicitly. The
	// dashboard's generic Free up space button leaves them off so a schedule
	// setting cannot silently turn a routine click into a volume prune.
	applyOptions := r.URL.Query().Get("applyOptions") == "1"
	result, err := deploy.RunDockerCleanup(r.Context(), s.docker, s.store, s.cfg, "manual", applyOptions)
	s.clearDockerUsageCache()
	if result.Reclaimed > 0 || result.ImagesRemoved > 0 {
		s.logger.Info("manual docker cleanup reclaimed storage",
			"bytes", result.Reclaimed, "images", result.ImagesRemoved, "sandbox", result.Sandbox)
	}
	s.audit(r.Context(), r, "docker.cleanup", "docker", "", map[string]any{
		"reclaimed":     result.Reclaimed,
		"imagesRemoved": result.ImagesRemoved,
		"volumes":       result.VolumesRemoved,
		"networks":      result.NetworksRemoved,
	})
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
