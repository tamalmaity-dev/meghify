package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// AppRepo persists applications together with their domains, env vars and volumes.
type AppRepo struct{ db *sql.DB }

// CreateApplication inserts an application record.
func (r *AppRepo) CreateApplication(ctx context.Context, a *Application) error {
	now := Text(time.Now().UTC())
	q := `INSERT INTO applications (id, environment_id, node_id, name, description, git_repository, git_branch,
	                                 build_pack, dockerfile, base_directory, port, health_check, status, fqdn,
	                                 image_tag, created_at, updated_at)
	      VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if _, err := r.db.ExecContext(ctx, q,
		a.ID, a.EnvironmentID, NullString(a.NodeID), a.Name, a.Description, a.GitRepository, a.GitBranch,
		a.BuildPack, a.Dockerfile, a.BaseDirectory, a.Port, a.HealthCheck, a.Status, a.FQDN,
		a.ImageTag, now, now); err != nil {
		return fmt.Errorf("insert application: %w", err)
	}
	a.CreatedAt, a.UpdatedAt = Parse(now), Parse(now)
	return nil
}

const appColumns = `id, environment_id, node_id, name, description, git_repository, git_branch,
                    build_pack, dockerfile, base_directory, port, health_check, status, fqdn, image_tag,
                    created_at, updated_at`

func scanApp(scanner interface{ Scan(...any) error }) (*Application, error) {
	var a Application
	var nodeID, createdAt, updatedAt sql.NullString
	err := scanner.Scan(&a.ID, &a.EnvironmentID, &nodeID, &a.Name, &a.Description, &a.GitRepository,
		&a.GitBranch, &a.BuildPack, &a.Dockerfile, &a.BaseDirectory, &a.Port, &a.HealthCheck,
		&a.Status, &a.FQDN, &a.ImageTag, &createdAt, &updatedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, err
	}
	// node_id is nullable because a resource can be unpinned from any server.
	a.NodeID = nullString(nodeID)
	a.CreatedAt, a.UpdatedAt = Parse(nullString(createdAt)), Parse(nullString(updatedAt))
	return &a, nil
}

// ApplicationByID loads an application.
func (r *AppRepo) ApplicationByID(ctx context.Context, id string) (*Application, error) {
	a, err := scanApp(r.db.QueryRowContext(ctx,
		`SELECT `+appColumns+` FROM applications WHERE id = ?`, id))
	if err != nil {
		return nil, fmt.Errorf("application %s: %w", id, err)
	}
	return a, nil
}

// ApplicationsByEnvironment lists the applications inside an environment.
func (r *AppRepo) ApplicationsByEnvironment(ctx context.Context, envID string) ([]*Application, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+appColumns+` FROM applications WHERE environment_id = ? ORDER BY created_at DESC`, envID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Application
	for rows.Next() {
		a, err := scanApp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListApplications returns every application, newest first.
func (r *AppRepo) ListApplications(ctx context.Context) ([]*Application, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+appColumns+` FROM applications ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Application
	for rows.Next() {
		a, err := scanApp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// UpdateApplication writes the full mutable application record.
func (r *AppRepo) UpdateApplication(ctx context.Context, a *Application) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE applications SET node_id = ?, name = ?, description = ?, git_repository = ?, git_branch = ?,
		                        build_pack = ?, dockerfile = ?, base_directory = ?, port = ?, health_check = ?,
		                        status = ?, fqdn = ?, image_tag = ?, updated_at = ?
		  WHERE id = ?`,
		NullString(a.NodeID), a.Name, a.Description, a.GitRepository, a.GitBranch,
		a.BuildPack, a.Dockerfile, a.BaseDirectory, a.Port, a.HealthCheck,
		a.Status, a.FQDN, a.ImageTag, Text(time.Now().UTC()), a.ID)
	if err != nil {
		return err
	}
	return rowsAffected(res)
}

// PatchApplicationStatus updates only the lifecycle state.
func (r *AppRepo) PatchApplicationStatus(ctx context.Context, id, status string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE applications SET status = ?, updated_at = ? WHERE id = ?`,
		status, Text(time.Now().UTC()), id)
	if err != nil {
		return err
	}
	return rowsAffected(res)
}

// DeleteApplication removes an application.
func (r *AppRepo) DeleteApplication(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM applications WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return rowsAffected(res)
}

// AssignApplicationNode pins an application to a node.
func (r *AppRepo) AssignApplicationNode(ctx context.Context, id, nodeID string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE applications SET node_id = ?, updated_at = ? WHERE id = ?`,
		NullString(nodeID), Text(time.Now().UTC()), id)
	if err != nil {
		return err
	}
	return rowsAffected(res)
}

// --- domains ---

// CreateDomain attaches a hostname to an application.
func (r *AppRepo) CreateDomain(ctx context.Context, d *Domain) error {
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO domains (id, application_id, host, tls_enabled, cert_status, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		d.ID, d.ApplicationID, d.Host, d.TLSEnabled, d.CertStatus, Text(time.Now().UTC())); err != nil {
		return fmt.Errorf("insert domain: %w", err)
	}
	return nil
}

// DomainsByApplication lists an application's hostnames.
func (r *AppRepo) DomainsByApplication(ctx context.Context, appID string) ([]*Domain, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, application_id, host, tls_enabled, cert_status, created_at
		   FROM domains WHERE application_id = ? ORDER BY created_at ASC`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Domain
	for rows.Next() {
		var d Domain
		var createdAt string
		if err := rows.Scan(&d.ID, &d.ApplicationID, &d.Host, &d.TLSEnabled, &d.CertStatus, &createdAt); err != nil {
			return nil, err
		}
		d.CreatedAt = Parse(createdAt)
		out = append(out, &d)
	}
	return out, rows.Err()
}

// ListDomains returns every domain.
func (r *AppRepo) ListDomains(ctx context.Context) ([]*Domain, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, application_id, host, tls_enabled, cert_status, created_at FROM domains ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Domain
	for rows.Next() {
		var d Domain
		var createdAt string
		if err := rows.Scan(&d.ID, &d.ApplicationID, &d.Host, &d.TLSEnabled, &d.CertStatus, &createdAt); err != nil {
			return nil, err
		}
		d.CreatedAt = Parse(createdAt)
		out = append(out, &d)
	}
	return out, rows.Err()
}

// DomainByHost resolves a hostname for Traefik config verification.
func (r *AppRepo) DomainByHost(ctx context.Context, host string) (*Domain, error) {
	var d Domain
	var createdAt string
	err := r.db.QueryRowContext(ctx,
		`SELECT id, application_id, host, tls_enabled, cert_status, created_at FROM domains WHERE host = ?`, host).
		Scan(&d.ID, &d.ApplicationID, &d.Host, &d.TLSEnabled, &d.CertStatus, &createdAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, err
	}
	d.CreatedAt = Parse(createdAt)
	return &d, nil
}

// SetDomainCertStatus records the outcome of a certificate request.
func (r *AppRepo) SetDomainCertStatus(ctx context.Context, id, status string) error {
	res, err := r.db.ExecContext(ctx, `UPDATE domains SET cert_status = ? WHERE id = ?`, status, id)
	if err != nil {
		return err
	}
	return rowsAffected(res)
}

// DeleteDomain removes a hostname.
func (r *AppRepo) DeleteDomain(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM domains WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return rowsAffected(res)
}

// --- env vars ---

// UpsertEnvVar inserts or refreshes a variable. The plaintext is expected to be
// already encrypted by the caller.
func (r *AppRepo) UpsertEnvVar(ctx context.Context, v *EnvVar) error {
	now := Text(time.Now().UTC())
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO env_vars (id, target, target_id, key, value_enc, is_secret, is_build, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (target, target_id, key, is_build)
		 DO UPDATE SET value_enc = excluded.value_enc, is_secret = excluded.is_secret`,
		v.ID, v.Target, v.TargetID, v.Key, v.ValueEnc, v.IsSecret, v.IsBuild, now)
	if err != nil {
		return fmt.Errorf("upsert env var: %w", err)
	}
	return nil
}

// EnvVarsByTarget returns the variables attached to a resource. Runtime and
// build variables are returned together; callers filter on IsBuild.
func (r *AppRepo) EnvVarsByTarget(ctx context.Context, target, targetID string) ([]*EnvVar, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, target, target_id, key, value_enc, is_secret, is_build, created_at
		   FROM env_vars WHERE target = ? AND target_id = ? ORDER BY key ASC`, target, targetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*EnvVar
	for rows.Next() {
		var v EnvVar
		var createdAt string
		if err := rows.Scan(&v.ID, &v.Target, &v.TargetID, &v.Key, &v.ValueEnc,
			&v.IsSecret, &v.IsBuild, &createdAt); err != nil {
			return nil, err
		}
		v.CreatedAt = Parse(createdAt)
		out = append(out, &v)
	}
	return out, rows.Err()
}

// DeleteEnvVar removes a single variable.
func (r *AppRepo) DeleteEnvVar(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM env_vars WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return rowsAffected(res)
}

// DeleteEnvVarsByTarget removes every variable for a resource.
func (r *AppRepo) DeleteEnvVarsByTarget(ctx context.Context, target, targetID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM env_vars WHERE target = ? AND target_id = ?`, target, targetID)
	return err
}

// --- volumes ---

// CreateVolume attaches persistent storage to a resource.
func (r *AppRepo) CreateVolume(ctx context.Context, v *Volume) error {
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO volumes (id, target, target_id, name, host_path, mount_path, read_only, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		v.ID, v.Target, v.TargetID, v.Name, v.HostPath, v.MountPath, v.ReadOnly, Text(time.Now().UTC())); err != nil {
		return fmt.Errorf("insert volume: %w", err)
	}
	return nil
}

// VolumesByTarget lists a resource's volumes.
func (r *AppRepo) VolumesByTarget(ctx context.Context, target, targetID string) ([]*Volume, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, target, target_id, name, host_path, mount_path, read_only, created_at
		   FROM volumes WHERE target = ? AND target_id = ? ORDER BY created_at ASC`, target, targetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Volume
	for rows.Next() {
		var v Volume
		var createdAt string
		if err := rows.Scan(&v.ID, &v.Target, &v.TargetID, &v.Name, &v.HostPath,
			&v.MountPath, &v.ReadOnly, &createdAt); err != nil {
			return nil, err
		}
		v.CreatedAt = Parse(createdAt)
		out = append(out, &v)
	}
	return out, rows.Err()
}

// DeleteVolume removes a volume binding.
func (r *AppRepo) DeleteVolume(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM volumes WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return rowsAffected(res)
}
