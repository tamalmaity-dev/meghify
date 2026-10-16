package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ResourceRepo persists Compose services, databases and backups.
type ResourceRepo struct{ db *sql.DB }

// --- services ---

// CreateService inserts a Compose service record.
func (r *ResourceRepo) CreateService(ctx context.Context, s *Service) error {
	now := Text(time.Now().UTC())
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO services (id, environment_id, node_id, name, kind, compose, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.EnvironmentID, NullString(s.NodeID), s.Name, s.Kind, s.Compose, s.Status, now, now); err != nil {
		return fmt.Errorf("insert service: %w", err)
	}
	s.CreatedAt, s.UpdatedAt = Parse(now), Parse(now)
	return nil
}

func scanService(scanner interface{ Scan(...any) error }) (*Service, error) {
	var s Service
	var nodeID, createdAt, updatedAt sql.NullString
	err := scanner.Scan(&s.ID, &s.EnvironmentID, &nodeID, &s.Name, &s.Kind, &s.Compose, &s.Status, &createdAt, &updatedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, err
	}
	s.NodeID = nullString(nodeID)
	s.CreatedAt, s.UpdatedAt = Parse(nullString(createdAt)), Parse(nullString(updatedAt))
	return &s, nil
}

// ServiceByID loads a service.
func (r *ResourceRepo) ServiceByID(ctx context.Context, id string) (*Service, error) {
	s, err := scanService(r.db.QueryRowContext(ctx,
		`SELECT id, environment_id, node_id, name, kind, compose, status, created_at, updated_at FROM services WHERE id = ?`, id))
	if err != nil {
		return nil, fmt.Errorf("service %s: %w", id, err)
	}
	return s, nil
}

// ServicesByEnvironment lists an environment's services.
func (r *ResourceRepo) ServicesByEnvironment(ctx context.Context, envID string) ([]*Service, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, environment_id, node_id, name, kind, compose, status, created_at, updated_at
		   FROM services WHERE environment_id = ? ORDER BY created_at DESC`, envID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Service
	for rows.Next() {
		s, err := scanService(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// UpdateService writes a service's compose definition and status.
func (r *ResourceRepo) UpdateService(ctx context.Context, s *Service) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE services SET node_id = ?, name = ?, kind = ?, compose = ?, status = ?, updated_at = ? WHERE id = ?`,
		NullString(s.NodeID), s.Name, s.Kind, s.Compose, s.Status, Text(time.Now().UTC()), s.ID)
	if err != nil {
		return err
	}
	return rowsAffected(res)
}

// PatchServiceStatus updates only the lifecycle state.
func (r *ResourceRepo) PatchServiceStatus(ctx context.Context, id, status string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE services SET status = ?, updated_at = ? WHERE id = ?`, status, Text(time.Now().UTC()), id)
	if err != nil {
		return err
	}
	return rowsAffected(res)
}

// DeleteService removes a service.
func (r *ResourceRepo) DeleteService(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM services WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return rowsAffected(res)
}

// --- databases ---

// CreateDatabase records a provisioned database.
func (r *ResourceRepo) CreateDatabase(ctx context.Context, d *Database) error {
	now := Text(time.Now().UTC())
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO databases (id, environment_id, node_id, name, kind, version, status, public_port, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.EnvironmentID, NullString(d.NodeID), d.Name, d.Kind, d.Version, d.Status, d.PublicPort, now, now); err != nil {
		return fmt.Errorf("insert database: %w", err)
	}
	d.CreatedAt, d.UpdatedAt = Parse(now), Parse(now)
	return nil
}

func scanDatabase(scanner interface{ Scan(...any) error }) (*Database, error) {
	var d Database
	var nodeID, createdAt, updatedAt sql.NullString
	err := scanner.Scan(&d.ID, &d.EnvironmentID, &nodeID, &d.Name, &d.Kind, &d.Version, &d.Status,
		&d.PublicPort, &createdAt, &updatedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, err
	}
	d.NodeID = nullString(nodeID)
	d.CreatedAt, d.UpdatedAt = Parse(nullString(createdAt)), Parse(nullString(updatedAt))
	return &d, nil
}

// DatabaseByID loads a database.
func (r *ResourceRepo) DatabaseByID(ctx context.Context, id string) (*Database, error) {
	d, err := scanDatabase(r.db.QueryRowContext(ctx,
		`SELECT id, environment_id, node_id, name, kind, version, status, public_port, created_at, updated_at
		   FROM databases WHERE id = ?`, id))
	if err != nil {
		return nil, fmt.Errorf("database %s: %w", id, err)
	}
	return d, nil
}

// DatabasesByEnvironment lists an environment's databases.
func (r *ResourceRepo) DatabasesByEnvironment(ctx context.Context, envID string) ([]*Database, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, environment_id, node_id, name, kind, version, status, public_port, created_at, updated_at
		   FROM databases WHERE environment_id = ? ORDER BY created_at DESC`, envID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Database
	for rows.Next() {
		d, err := scanDatabase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ListDatabases returns every database.
func (r *ResourceRepo) ListDatabases(ctx context.Context) ([]*Database, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, environment_id, node_id, name, kind, version, status, public_port, created_at, updated_at
		   FROM databases ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Database
	for rows.Next() {
		d, err := scanDatabase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// PatchDatabaseStatus updates the database state.
func (r *ResourceRepo) PatchDatabaseStatus(ctx context.Context, id, status string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE databases SET status = ?, updated_at = ? WHERE id = ?`, status, Text(time.Now().UTC()), id)
	if err != nil {
		return err
	}
	return rowsAffected(res)
}

// DeleteDatabase removes a database record.
func (r *ResourceRepo) DeleteDatabase(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM databases WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return rowsAffected(res)
}

// --- backups ---

func scanBackup(scanner interface{ Scan(...any) error }) (*Backup, error) {
	var b Backup
	var lastRun, createdAt sql.NullString
	if err := scanner.Scan(&b.ID, &b.Target, &b.TargetID, &b.Destination, &b.Status, &b.SizeBytes,
		&b.Error, &b.Schedule, &lastRun, &createdAt); err != nil {
		return nil, err
	}
	b.LastRunAt = optionalTime(lastRun)
	b.CreatedAt = Parse(nullString(createdAt))
	return &b, nil
}

// CreateBackup schedules or records a backup, running it once immediately.
func (r *ResourceRepo) CreateBackup(ctx context.Context, b *Backup) error {
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO backups (id, target, target_id, destination, status, size_bytes, error, schedule, last_run_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		b.ID, b.Target, b.TargetID, b.Destination, b.Status, b.SizeBytes, b.Error,
		b.Schedule, TextOptional(b.LastRunAt), Text(time.Now().UTC())); err != nil {
		return fmt.Errorf("insert backup: %w", err)
	}
	return nil
}

func (r *ResourceRepo) BackupsByTarget(ctx context.Context, target, targetID string) ([]*Backup, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, target, target_id, destination, status, size_bytes, error, schedule, last_run_at, created_at
		   FROM backups WHERE target = ? AND target_id = ? ORDER BY created_at DESC`, target, targetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Backup
	for rows.Next() {
		b, err := scanBackup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ScheduledBackups returns every backup that has a cron schedule attached.
func (r *ResourceRepo) ScheduledBackups(ctx context.Context) ([]*Backup, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, target, target_id, destination, status, size_bytes, error, schedule, last_run_at, created_at
		   FROM backups WHERE schedule != '' ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Backup
	for rows.Next() {
		b, err := scanBackup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// FinishBackup records the terminal state of a backup run.
func (r *ResourceRepo) FinishBackup(ctx context.Context, id, status string, size int64, errMsg string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE backups SET status = ?, size_bytes = ?, error = ?, last_run_at = ? WHERE id = ?`,
		status, size, errMsg, Text(time.Now().UTC()), id)
	if err != nil {
		return err
	}
	return rowsAffected(res)
}

// BackupByID loads a single backup.
func (r *ResourceRepo) BackupByID(ctx context.Context, id string) (*Backup, error) {
	backup, err := scanBackup(r.db.QueryRowContext(ctx,
		`SELECT id, target, target_id, destination, status, size_bytes, error, schedule, last_run_at, created_at
		   FROM backups WHERE id = ?`, id))
	if err != nil {
		return nil, fmt.Errorf("backup %s: %w", id, err)
	}
	return backup, nil
}

// DeleteBackup removes a backup record.
func (r *ResourceRepo) DeleteBackup(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM backups WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return rowsAffected(res)
}
