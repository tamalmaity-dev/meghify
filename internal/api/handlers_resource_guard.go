package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/tamalmaity-dev/meghify/internal/db"
)

// resourceGuardSettings returns the panel-wide protection policy.
func (s *Server) resourceGuardSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.ResourceGuard.Settings(r.Context())
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

// updateResourceGuardSettings validates and stores the policy. The scheduler
// reads the same row on every pass, so changes apply without a restart.
func (s *Server) updateResourceGuardSettings(w http.ResponseWriter, r *http.Request) {
	var body db.ResourceGuardSettings
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateResourceGuardSettings(&body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.ResourceGuard.SaveSettings(r.Context(), &body); err != nil {
		mapError(w, err)
		return
	}
	if s.guard != nil {
		s.guard.ResetAll()
	}
	s.audit(r.Context(), r, "resource_guard.settings", "resource_guard", "panel", map[string]any{
		"enabled":      body.Enabled,
		"cpuAction":    body.CPUAction,
		"memoryAction": body.MemoryAction,
	})
	writeJSON(w, http.StatusOK, body)
}

func validateResourceGuardSettings(settings *db.ResourceGuardSettings) error {
	defaults := db.DefaultResourceGuardSettings()
	settings.CPUAction = strings.TrimSpace(settings.CPUAction)
	settings.MemoryAction = strings.TrimSpace(settings.MemoryAction)
	if settings.CPUAction == "" {
		settings.CPUAction = defaults.CPUAction
	}
	if settings.MemoryAction == "" {
		settings.MemoryAction = defaults.MemoryAction
	}
	if settings.CPUPercent == 0 {
		settings.CPUPercent = defaults.CPUPercent
	}
	if settings.CPUDurationSeconds == 0 {
		settings.CPUDurationSeconds = defaults.CPUDurationSeconds
	}
	if settings.CPUThrottlePercent == 0 {
		settings.CPUThrottlePercent = defaults.CPUThrottlePercent
	}
	if settings.MemoryPercent == 0 {
		settings.MemoryPercent = defaults.MemoryPercent
	}
	if settings.MemoryDurationSeconds == 0 {
		settings.MemoryDurationSeconds = defaults.MemoryDurationSeconds
	}
	if settings.HostMemoryPercent == 0 {
		settings.HostMemoryPercent = defaults.HostMemoryPercent
	}
	if settings.HostMemoryDurationSecs == 0 {
		settings.HostMemoryDurationSecs = defaults.HostMemoryDurationSecs
	}

	if settings.CPUPercent < 1 || settings.CPUPercent > 100 {
		return fmt.Errorf("CPU threshold must be between 1 and 100 percent")
	}
	if settings.CPUDurationSeconds < 10 || settings.CPUDurationSeconds > 86400 {
		return fmt.Errorf("CPU duration must be between 10 seconds and 24 hours")
	}
	switch settings.CPUAction {
	case db.GuardActionNotify, db.GuardActionThrottle, db.GuardActionStop:
	default:
		return fmt.Errorf("CPU action must be %q, %q or %q",
			db.GuardActionNotify, db.GuardActionThrottle, db.GuardActionStop)
	}
	if settings.CPUThrottlePercent < 1 || settings.CPUThrottlePercent > 99 {
		return fmt.Errorf("CPU throttle level must be between 1 and 99 percent")
	}
	if settings.CPUAction == db.GuardActionThrottle && settings.CPUThrottlePercent >= settings.CPUPercent {
		return fmt.Errorf("CPU throttle level must be below the CPU threshold, otherwise throttling cannot relieve pressure")
	}
	if settings.MemoryPercent < 1 || settings.MemoryPercent > 100 {
		return fmt.Errorf("memory threshold must be between 1 and 100 percent")
	}
	if settings.MemoryDurationSeconds < 10 || settings.MemoryDurationSeconds > 86400 {
		return fmt.Errorf("memory duration must be between 10 seconds and 24 hours")
	}
	switch settings.MemoryAction {
	case db.GuardActionNotify, db.GuardActionStop:
	default:
		return fmt.Errorf("memory action must be %q or %q", db.GuardActionNotify, db.GuardActionStop)
	}
	if settings.HostMemoryPercent < 1 || settings.HostMemoryPercent > 100 {
		return fmt.Errorf("host memory threshold must be between 1 and 100 percent")
	}
	if settings.HostMemoryDurationSecs < 10 || settings.HostMemoryDurationSecs > 86400 {
		return fmt.Errorf("host memory duration must be between 10 seconds and 24 hours")
	}
	if settings.GraceSeconds < 0 || settings.GraceSeconds > 86400 {
		return fmt.Errorf("start grace period must be between 0 and 24 hours")
	}
	if settings.CooldownSeconds < 0 || settings.CooldownSeconds > 604800 {
		return fmt.Errorf("cooldown must be between 0 and 7 days")
	}
	return nil
}

// resourceGuardEvents powers the server-page history and the application
// runtime card. An application filter keeps the latter focused.
func (s *Server) resourceGuardEvents(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	events, err := s.store.ResourceGuard.Events(r.Context(), r.URL.Query().Get("applicationId"), limit)
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, events)
}
