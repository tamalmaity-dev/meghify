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
	                                 image_tag, github_app_id, github_installation_id, repository_full_name,
	                                 auto_deploy, port_mappings, redirect_https, cpu_limit, memory_limit,
	                                 pre_deploy_command, post_deploy_command, created_at, updated_at)
	      VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if _, err := r.db.ExecContext(ctx, q,
		a.ID, a.EnvironmentID, NullString(a.NodeID), a.Name, a.Description, a.GitRepository, a.GitBranch,
		a.BuildPack, a.Dockerfile, a.BaseDirectory, a.Port, a.HealthCheck, a.Status, a.FQDN,
		a.ImageTag, NullString(a.GitHubAppID), a.GitHubInstallationID, a.RepositoryFullName,
		a.AutoDeploy, a.PortMappings, a.RedirectHTTPS, a.CPULimit, a.MemoryLimit, a.PreDeployCommand,
		a.PostDeployCommand, now, now); err != nil {
		return fmt.Errorf("insert application: %w", err)
	}
	a.CreatedAt, a.UpdatedAt = Parse(now), Parse(now)
	return nil
}

const appColumns = `id, environment_id, node_id, name, description, git_repository, git_branch,
                    build_pack, dockerfile, base_directory, port, health_check, status, fqdn, image_tag,
                    github_app_id, github_installation_id, repository_full_name, auto_deploy,
                    port_mappings, redirect_https, cpu_limit, memory_limit, pre_deploy_command,
                    post_deploy_command, created_at, updated_at`

func scanApp(scanner interface{ Scan(...any) error }) (*Application, error) {
	var a Application
	var nodeID, githubAppID, createdAt, updatedAt sql.NullString
	err := scanner.Scan(&a.ID, &a.EnvironmentID, &nodeID, &a.Name, &a.Description, &a.GitRepository,
		&a.GitBranch, &a.BuildPack, &a.Dockerfile, &a.BaseDirectory, &a.Port, &a.HealthCheck,
		&a.Status, &a.FQDN, &a.ImageTag, &githubAppID, &a.GitHubInstallationID, &a.RepositoryFullName,
		&a.AutoDeploy, &a.PortMappings, &a.RedirectHTTPS, &a.CPULimit, &a.MemoryLimit,
		&a.PreDeployCommand, &a.PostDeployCommand, &createdAt, &updatedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, err
	}
	// node_id is nullable because a resource can be unpinned from any server.
	a.NodeID = nullString(nodeID)
	a.GitHubAppID = nullString(githubAppID)
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
		                        status = ?, fqdn = ?, image_tag = ?, github_app_id = ?,
		                        github_installation_id = ?, repository_full_name = ?, auto_deploy = ?,
		                        port_mappings = ?, redirect_https = ?, cpu_limit = ?, memory_limit = ?,
		                        pre_deploy_command = ?, post_deploy_command = ?, updated_at = ?
		  WHERE id = ?`,
		NullString(a.NodeID), a.Name, a.Description, a.GitRepository, a.GitBranch,
		a.BuildPack, a.Dockerfile, a.BaseDirectory, a.Port, a.HealthCheck,
		a.Status, a.FQDN, a.ImageTag, NullString(a.GitHubAppID),
		a.GitHubInstallationID, a.RepositoryFullName, a.AutoDeploy,
		a.PortMappings, a.RedirectHTTPS, a.CPULimit, a.MemoryLimit, a.PreDeployCommand,
		a.PostDeployCommand, Text(time.Now().UTC()), a.ID)
	if err != nil {
		return err
	}
	return rowsAffected(res)
}

// AutoDeployApplications lists the applications wired to a repository that
// should redeploy when a push lands on the given branch.
func (r *AppRepo) AutoDeployApplications(ctx context.Context, githubAppID, fullName, branch string) ([]*Application, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+appColumns+` FROM applications
		  WHERE auto_deploy AND github_app_id = ? AND repository_full_name = ? AND git_branch = ?
		  ORDER BY created_at DESC`, githubAppID, fullName, branch)
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

// PatchApplicationImage records the image tag the latest deployment produced,
// so the UI can show what is actually running.
func (r *AppRepo) PatchApplicationImage(ctx context.Context, id, imageTag string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE applications SET image_tag = ?, updated_at = ? WHERE id = ?`,
		imageTag, Text(time.Now().UTC()), id)
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

const domainColumns = `id, application_id, host, tls_enabled, cert_status, path, port,
                      redirect_https, search_indexing, dns_status, dns_checked_at, created_at`

func scanDomain(scanner interface{ Scan(...any) error }) (*Domain, error) {
	var d Domain
	var dnsCheckedAt, createdAt sql.NullString
	err := scanner.Scan(&d.ID, &d.ApplicationID, &d.Host, &d.TLSEnabled, &d.CertStatus,
		&d.Path, &d.Port, &d.RedirectHTTPS, &d.SearchIndexing, &d.DNSStatus,
		&dnsCheckedAt, &createdAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, err
	}
	d.DNSCheckedAt = optionalTime(dnsCheckedAt)
	d.CreatedAt = Parse(nullString(createdAt))
	return &d, nil
}

// CreateDomain attaches a hostname to an application.
func (r *AppRepo) CreateDomain(ctx context.Context, d *Domain) error {
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO domains (id, application_id, host, tls_enabled, cert_status, path, port,
		                      redirect_https, search_indexing, dns_status, dns_checked_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.ApplicationID, d.Host, d.TLSEnabled, d.CertStatus, d.Path, d.Port,
		d.RedirectHTTPS, d.SearchIndexing, d.DNSStatus, TextOptional(d.DNSCheckedAt),
		Text(time.Now().UTC())); err != nil {
		return fmt.Errorf("insert domain: %w", err)
	}
	return nil
}

// UpdateDomain writes the full mutable domain record.
func (r *AppRepo) UpdateDomain(ctx context.Context, d *Domain) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE domains SET host = ?, tls_enabled = ?, cert_status = ?, path = ?, port = ?,
		                    redirect_https = ?, search_indexing = ?, dns_status = ?, dns_checked_at = ?
		  WHERE id = ?`,
		d.Host, d.TLSEnabled, d.CertStatus, d.Path, d.Port,
		d.RedirectHTTPS, d.SearchIndexing, d.DNSStatus, TextOptional(d.DNSCheckedAt), d.ID)
	if err != nil {
		return err
	}
	return rowsAffected(res)
}

// DomainsByApplication lists an application's hostnames.
func (r *AppRepo) DomainsByApplication(ctx context.Context, appID string) ([]*Domain, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+domainColumns+` FROM domains WHERE application_id = ? ORDER BY created_at ASC`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Domain
	for rows.Next() {
		d, err := scanDomain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DomainByID loads a hostname.
func (r *AppRepo) DomainByID(ctx context.Context, id string) (*Domain, error) {
	d, err := scanDomain(r.db.QueryRowContext(ctx,
		`SELECT `+domainColumns+` FROM domains WHERE id = ?`, id))
	if err != nil {
		return nil, fmt.Errorf("domain %s: %w", id, err)
	}
	return d, nil
}

// ListDomains returns every domain.
func (r *AppRepo) ListDomains(ctx context.Context) ([]*Domain, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+domainColumns+` FROM domains ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Domain
	for rows.Next() {
		d, err := scanDomain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DomainByHost resolves a hostname for Traefik config verification.
func (r *AppRepo) DomainByHost(ctx context.Context, host string) (*Domain, error) {
	d, err := scanDomain(r.db.QueryRowContext(ctx,
		`SELECT `+domainColumns+` FROM domains WHERE host = ?`, host))
	if err != nil {
		return nil, fmt.Errorf("domain %s: %w", host, err)
	}
	return d, nil
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

// VolumeByID loads one volume record.
func (r *AppRepo) VolumeByID(ctx context.Context, id string) (*Volume, error) {
	var v Volume
	var createdAt string
	err := r.db.QueryRowContext(ctx,
		`SELECT id, target, target_id, name, host_path, mount_path, read_only, created_at
		   FROM volumes WHERE id = ?`, id).
		Scan(&v.ID, &v.Target, &v.TargetID, &v.Name, &v.HostPath, &v.MountPath, &v.ReadOnly, &createdAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, err
	}
	v.CreatedAt = Parse(createdAt)
	return &v, nil
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
