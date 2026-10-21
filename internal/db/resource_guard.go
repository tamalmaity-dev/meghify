package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Resource guard action, metric and event-state values shared by the
// scheduler, API and UI.
const (
	GuardActionNotify   = "notify"
	GuardActionThrottle = "throttle"
	GuardActionStop     = "stop"

	GuardMetricCPU        = "cpu"
	GuardMetricMemory     = "memory"
	GuardMetricHostMemory = "host_memory"

	GuardStatusSuccess = "success"
	GuardStatusFailed  = "failed"
)

// ResourceGuardSettings is the panel-wide resource protection configuration.
// The guard samples application containers on every managed server; these
// defaults apply unless an application overrides them.
type ResourceGuardSettings struct {
	Enabled                bool      `json:"enabled"`
	CPUEnabled             bool      `json:"cpuEnabled"`
	CPUPercent             int       `json:"cpuPercent"`
	CPUDurationSeconds     int       `json:"cpuDurationSeconds"`
	CPUAction              string    `json:"cpuAction"`
	CPUThrottlePercent     int       `json:"cpuThrottlePercent"`
	MemoryEnabled          bool      `json:"memoryEnabled"`
	MemoryPercent          int       `json:"memoryPercent"`
	MemoryDurationSeconds  int       `json:"memoryDurationSeconds"`
	MemoryAction           string    `json:"memoryAction"`
	HostMemoryEnabled      bool      `json:"hostMemoryEnabled"`
	HostMemoryPercent      int       `json:"hostMemoryPercent"`
	HostMemoryDurationSecs int       `json:"hostMemoryDurationSeconds"`
	GraceSeconds           int       `json:"graceSeconds"`
	CooldownSeconds        int       `json:"cooldownSeconds"`
	UpdatedAt              time.Time `json:"updatedAt"`
}

// DefaultResourceGuardSettings is used until the operator saves a policy.
// CPU defaults to throttle+notify (the least destructive useful action);
// memory defaults to stop because a full host is dangerous to every workload.
func DefaultResourceGuardSettings() ResourceGuardSettings {
	return ResourceGuardSettings{
		Enabled:                true,
		CPUEnabled:             true,
		CPUPercent:             90,
		CPUDurationSeconds:     600,
		CPUAction:              GuardActionThrottle,
		CPUThrottlePercent:     50,
		MemoryEnabled:          true,
		MemoryPercent:          90,
		MemoryDurationSeconds:  300,
		MemoryAction:           GuardActionStop,
		HostMemoryEnabled:      true,
		HostMemoryPercent:      95,
		HostMemoryDurationSecs: 120,
		GraceSeconds:           300,
		CooldownSeconds:        900,
	}
}

// ResourceGuardEvent is one decision or attempted action, shown in the server
// page and application runtime card.
type ResourceGuardEvent struct {
	ID               string    `json:"id"`
	ApplicationID    string    `json:"applicationId"`
	ApplicationName  string    `json:"applicationName"`
	NodeID           string    `json:"nodeId"`
	ContainerID      string    `json:"containerId"`
	ContainerName    string    `json:"containerName"`
	Metric           string    `json:"metric"`
	Action           string    `json:"action"`
	Status           string    `json:"status"`
	ObservedPercent  float64   `json:"observedPercent"`
	ThresholdPercent int       `json:"thresholdPercent"`
	DurationSeconds  int       `json:"durationSeconds"`
	UsageBytes       int64     `json:"usageBytes,omitempty"`
	LimitBytes       int64     `json:"limitBytes,omitempty"`
	Message          string    `json:"message"`
	Error            string    `json:"error,omitempty"`
	CreatedAt        time.Time `json:"createdAt"`
}

// ResourceGuardRepo persists the singleton guard policy and its action history.
type ResourceGuardRepo struct{ db *sql.DB }

// Settings returns the singleton policy, creating defaults on first use.
func (r *ResourceGuardRepo) Settings(ctx context.Context) (*ResourceGuardSettings, error) {
	now := Text(time.Now().UTC())
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO resource_guard_settings (id, updated_at) VALUES ('panel', ?)
		 ON CONFLICT (id) DO NOTHING`, now); err != nil {
		return nil, fmt.Errorf("ensure resource guard settings: %w", err)
	}

	var s ResourceGuardSettings
	var updated string
	err := r.db.QueryRowContext(ctx,
		`SELECT enabled, cpu_enabled, cpu_percent, cpu_duration_seconds, cpu_action,
		        cpu_throttle_percent, memory_enabled, memory_percent,
		        memory_duration_seconds, memory_action, host_memory_enabled,
		        host_memory_percent, host_memory_duration_seconds, grace_seconds,
		        cooldown_seconds, updated_at
		   FROM resource_guard_settings WHERE id = 'panel'`).
		Scan(&s.Enabled, &s.CPUEnabled, &s.CPUPercent, &s.CPUDurationSeconds,
			&s.CPUAction, &s.CPUThrottlePercent, &s.MemoryEnabled, &s.MemoryPercent,
			&s.MemoryDurationSeconds, &s.MemoryAction, &s.HostMemoryEnabled,
			&s.HostMemoryPercent, &s.HostMemoryDurationSecs, &s.GraceSeconds,
			&s.CooldownSeconds, &updated)
	if err != nil {
		return nil, fmt.Errorf("read resource guard settings: %w", err)
	}
	s.UpdatedAt = Parse(updated)
	return &s, nil
}

// SaveSettings writes the singleton policy.
func (r *ResourceGuardRepo) SaveSettings(ctx context.Context, s *ResourceGuardSettings) error {
	if s == nil {
		return errors.New("resource guard settings are required")
	}
	now := Text(time.Now().UTC())
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO resource_guard_settings (id, updated_at) VALUES ('panel', ?)
		 ON CONFLICT (id) DO NOTHING`, now); err != nil {
		return fmt.Errorf("ensure resource guard settings: %w", err)
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE resource_guard_settings
		    SET enabled = ?, cpu_enabled = ?, cpu_percent = ?, cpu_duration_seconds = ?,
		        cpu_action = ?, cpu_throttle_percent = ?, memory_enabled = ?,
		        memory_percent = ?, memory_duration_seconds = ?, memory_action = ?,
		        host_memory_enabled = ?, host_memory_percent = ?,
		        host_memory_duration_seconds = ?, grace_seconds = ?,
		        cooldown_seconds = ?, updated_at = ?
		  WHERE id = 'panel'`,
		s.Enabled, s.CPUEnabled, s.CPUPercent, s.CPUDurationSeconds, s.CPUAction,
		s.CPUThrottlePercent, s.MemoryEnabled, s.MemoryPercent,
		s.MemoryDurationSeconds, s.MemoryAction, s.HostMemoryEnabled,
		s.HostMemoryPercent, s.HostMemoryDurationSecs, s.GraceSeconds,
		s.CooldownSeconds, now)
	if err != nil {
		return fmt.Errorf("save resource guard settings: %w", err)
	}
	if err := rowsAffected(res); err != nil {
		return err
	}
	s.UpdatedAt = Parse(now)
	return nil
}

// AddEvent records one guard decision.
func (r *ResourceGuardRepo) AddEvent(ctx context.Context, event *ResourceGuardEvent) error {
	if event == nil {
		return errors.New("resource guard event is required")
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO resource_guard_events
		    (id, application_id, application_name, node_id, container_id,
		     container_name, metric, action, status, observed_percent,
		     threshold_percent, duration_seconds, usage_bytes, limit_bytes,
		     message, error, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID, event.ApplicationID, event.ApplicationName, event.NodeID,
		event.ContainerID, event.ContainerName, event.Metric, event.Action,
		event.Status, event.ObservedPercent, event.ThresholdPercent,
		event.DurationSeconds, event.UsageBytes, event.LimitBytes,
		event.Message, event.Error, Text(event.CreatedAt))
	if err != nil {
		return fmt.Errorf("insert resource guard event: %w", err)
	}
	return nil
}

// Events returns recent decisions, optionally scoped to one application.
func (r *ResourceGuardRepo) Events(ctx context.Context, appID string, limit int) ([]*ResourceGuardEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	query := `SELECT id, application_id, application_name, node_id, container_id,
	                 container_name, metric, action, status, observed_percent,
	                 threshold_percent, duration_seconds, usage_bytes, limit_bytes,
	                 message, error, created_at
	            FROM resource_guard_events`
	args := make([]any, 0, 2)
	if strings.TrimSpace(appID) != "" {
		query += ` WHERE application_id = ?`
		args = append(args, strings.TrimSpace(appID))
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list resource guard events: %w", err)
	}
	defer rows.Close()

	out := make([]*ResourceGuardEvent, 0, limit)
	for rows.Next() {
		var event ResourceGuardEvent
		var created string
		if err := rows.Scan(&event.ID, &event.ApplicationID, &event.ApplicationName,
			&event.NodeID, &event.ContainerID, &event.ContainerName, &event.Metric,
			&event.Action, &event.Status, &event.ObservedPercent,
			&event.ThresholdPercent, &event.DurationSeconds, &event.UsageBytes,
			&event.LimitBytes, &event.Message, &event.Error, &created); err != nil {
			return nil, err
		}
		event.CreatedAt = Parse(created)
		out = append(out, &event)
	}
	return out, rows.Err()
}

// PruneEvents keeps the action history bounded.
func (r *ResourceGuardRepo) PruneEvents(ctx context.Context, before time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM resource_guard_events WHERE created_at < ?`, Text(before))
	if err != nil {
		return 0, fmt.Errorf("prune resource guard events: %w", err)
	}
	return res.RowsAffected()
}
