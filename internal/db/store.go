package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// Store is the single access point to every repository. Handlers receive this
// instead of a raw *sql.DB so the driver (sqlite or postgres) stays an
// implementation detail.
type Store struct {
	db *sql.DB

	Users         *UserRepo
	Nodes         *NodeRepo
	Projects      *ProjectRepo
	Applications  *AppRepo
	Resources     *ResourceRepo
	Deployments   *DeploymentRepo
	Jobs          *JobRepo
	GitHub        *GitHubRepo
	DockerCleanup *DockerCleanupRepo
	ResourceGuard *ResourceGuardRepo
}

// OpenStore builds a Store from an already opened handle.
func OpenStore(handle *sql.DB) *Store {
	return &Store{
		db:            handle,
		Users:         &UserRepo{db: handle},
		Nodes:         &NodeRepo{db: handle},
		Projects:      &ProjectRepo{db: handle},
		Applications:  &AppRepo{db: handle},
		Resources:     &ResourceRepo{db: handle},
		Deployments:   &DeploymentRepo{db: handle},
		Jobs:          &JobRepo{db: handle},
		GitHub:        &GitHubRepo{db: handle},
		DockerCleanup: &DockerCleanupRepo{db: handle},
		ResourceGuard: &ResourceGuardRepo{db: handle},
	}
}

// Handle exposes the raw database for callers that need direct SQL.
func (s *Store) Handle() *sql.DB { return s.db }

// --- settings ---

// GetSetting reads an instance setting. Missing keys return "".
func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

// SetSetting writes an instance setting.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, Text(time.Now().UTC()))
	return err
}

// AllSettings returns every instance setting.
func (s *Store) AllSettings(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// --- audit log ---

// Audit records a privileged action. Failures are non-fatal by design: an audit
// write should never take down the request that produced it.
func (s *Store) Audit(ctx context.Context, entry *AuditLog) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO audit_logs (id, user_id, action, entity_type, entity_id, metadata, ip, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.ID, NullString(entry.UserID), entry.Action, entry.EntityType, entry.EntityID,
		orJSON(entry.Metadata), entry.IP, Text(time.Now().UTC()))
	return err
}

// AuditLogs returns recent audit entries.
func (s *Store) AuditLogs(ctx context.Context, limit int) ([]*AuditLog, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, action, entity_type, entity_id, metadata, ip, created_at
		   FROM audit_logs ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*AuditLog
	for rows.Next() {
		var a AuditLog
		var userID, createdAt string
		if err := rows.Scan(&a.ID, &userID, &a.Action, &a.EntityType, &a.EntityID,
			&a.Metadata, &a.IP, &createdAt); err != nil {
			return nil, err
		}
		a.UserID = userID
		a.CreatedAt = Parse(createdAt)
		out = append(out, &a)
	}
	return out, rows.Err()
}

// --- webhooks ---

// CreateWebhook registers an inbound deploy trigger.
func (s *Store) CreateWebhook(ctx context.Context, w *Webhook) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO webhooks (id, application_id, provider, secret, created_at) VALUES (?, ?, ?, ?, ?)`,
		w.ID, w.ApplicationID, w.Provider, w.Secret, Text(time.Now().UTC()))
	return err
}

// WebhookByApplication returns the webhook registered for an application.
func (s *Store) WebhookByApplication(ctx context.Context, appID string) (*Webhook, error) {
	var w Webhook
	var createdAt string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, application_id, provider, secret, created_at FROM webhooks WHERE application_id = ?`, appID).
		Scan(&w.ID, &w.ApplicationID, &w.Provider, &w.Secret, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	w.CreatedAt = Parse(createdAt)
	return &w, nil
}

// DeleteWebhook removes a webhook.
func (s *Store) DeleteWebhook(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM webhooks WHERE id = ?`, id)
	return err
}

// --- stats ---

// Stats is the dashboard summary payload.
type Stats struct {
	Projects     int `json:"projects"`
	Applications int `json:"applications"`
	Databases    int `json:"databases"`
	Services     int `json:"services"`
	Nodes        int `json:"nodes"`
	OnlineNodes  int `json:"onlineNodes"`
	Deployments  int `json:"deployments"`
	PendingJobs  int `json:"pendingJobs"`
	Users        int `json:"users"`
}

// Stats collects dashboard counters in a single pass.
func (s *Store) Stats(ctx context.Context) (*Stats, error) {
	count := func(table, where string) (int, error) {
		var n int
		q := "SELECT COUNT(*) FROM " + table
		if where != "" {
			q += " WHERE " + where
		}
		err := s.db.QueryRowContext(ctx, q).Scan(&n)
		return n, err
	}

	var out Stats
	var err error
	if out.Projects, err = count("projects", ""); err != nil {
		return nil, err
	}
	if out.Applications, err = count("applications", ""); err != nil {
		return nil, err
	}
	if out.Databases, err = count("databases", ""); err != nil {
		return nil, err
	}
	if out.Services, err = count("services", ""); err != nil {
		return nil, err
	}
	if out.Nodes, err = count("nodes", ""); err != nil {
		return nil, err
	}
	if out.OnlineNodes, err = count("nodes", "status = 'online'"); err != nil {
		return nil, err
	}
	if out.Users, err = count("users", ""); err != nil {
		return nil, err
	}
	if out.PendingJobs, err = s.Jobs.PendingCount(ctx); err != nil {
		return nil, err
	}
	return &out, nil
}

func orJSON(s string) string {
	if s == "" {
		return "{}"
	}
	var probe any
	if json.Unmarshal([]byte(s), &probe) != nil {
		return "{}"
	}
	return s
}
