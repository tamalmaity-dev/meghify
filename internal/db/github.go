package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// GitHubRepo persists GitHub App sources and the accounts that installed them.
type GitHubRepo struct{ db *sql.DB }

const githubAppColumns = `id, name, slug, app_id, client_id, client_secret_enc, private_key_enc,
                         webhook_secret_enc, html_url, base_url, organization, state, created_at, updated_at`

func scanGitHubApp(scanner interface{ Scan(...any) error }) (*GitHubApp, error) {
	var a GitHubApp
	var createdAt, updatedAt string
	err := scanner.Scan(&a.ID, &a.Name, &a.Slug, &a.AppID, &a.ClientID, &a.ClientSecretEnc,
		&a.PrivateKeyEnc, &a.WebhookSecretEnc, &a.HTMLURL, &a.BaseURL, &a.Organization,
		&a.State, &createdAt, &updatedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, err
	}
	a.CreatedAt, a.UpdatedAt = Parse(createdAt), Parse(updatedAt)
	return &a, nil
}

// CreateGitHubApp inserts a source.
func (r *GitHubRepo) CreateGitHubApp(ctx context.Context, a *GitHubApp) error {
	now := Text(time.Now().UTC())
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO github_apps (`+githubAppColumns+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.Name, a.Slug, a.AppID, a.ClientID, a.ClientSecretEnc, a.PrivateKeyEnc,
		a.WebhookSecretEnc, a.HTMLURL, a.BaseURL, a.Organization, a.State, now, now); err != nil {
		return fmt.Errorf("insert github app: %w", err)
	}
	a.CreatedAt, a.UpdatedAt = Parse(now), Parse(now)
	return nil
}

// UpdateGitHubApp writes the full record.
func (r *GitHubRepo) UpdateGitHubApp(ctx context.Context, a *GitHubApp) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE github_apps SET name = ?, slug = ?, app_id = ?, client_id = ?, client_secret_enc = ?,
		        private_key_enc = ?, webhook_secret_enc = ?, html_url = ?, base_url = ?,
		        organization = ?, state = ?, updated_at = ? WHERE id = ?`,
		a.Name, a.Slug, a.AppID, a.ClientID, a.ClientSecretEnc, a.PrivateKeyEnc,
		a.WebhookSecretEnc, a.HTMLURL, a.BaseURL, a.Organization, a.State,
		Text(time.Now().UTC()), a.ID)
	if err != nil {
		return err
	}
	return rowsAffected(res)
}

// GitHubAppByID loads a source.
func (r *GitHubRepo) GitHubAppByID(ctx context.Context, id string) (*GitHubApp, error) {
	a, err := scanGitHubApp(r.db.QueryRowContext(ctx,
		`SELECT `+githubAppColumns+` FROM github_apps WHERE id = ?`, id))
	if err != nil {
		return nil, fmt.Errorf("github app %s: %w", id, err)
	}
	return a, nil
}

// GitHubAppByState resolves the pending manifest-flow app for a callback.
func (r *GitHubRepo) GitHubAppByState(ctx context.Context, state string) (*GitHubApp, error) {
	if state == "" {
		return nil, ErrNotFound
	}
	a, err := scanGitHubApp(r.db.QueryRowContext(ctx,
		`SELECT `+githubAppColumns+` FROM github_apps WHERE state = ?`, state))
	if err != nil {
		return nil, fmt.Errorf("github app for state: %w", err)
	}
	return a, nil
}

// GitHubAppByAppID resolves a source from GitHub's numeric app id, which every
// webhook delivery carries as the installation target id.
func (r *GitHubRepo) GitHubAppByAppID(ctx context.Context, appID int64) (*GitHubApp, error) {
	a, err := scanGitHubApp(r.db.QueryRowContext(ctx,
		`SELECT `+githubAppColumns+` FROM github_apps WHERE app_id = ?`, appID))
	if err != nil {
		return nil, fmt.Errorf("github app %d: %w", appID, err)
	}
	return a, nil
}

// ListGitHubApps returns every source, newest first.
func (r *GitHubRepo) ListGitHubApps(ctx context.Context) ([]*GitHubApp, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+githubAppColumns+` FROM github_apps ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*GitHubApp
	for rows.Next() {
		a, err := scanGitHubApp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DeleteGitHubApp removes a source. Applications keep their repository URL and
// deployment history; they only lose the link that enabled automatic deploys.
func (r *GitHubRepo) DeleteGitHubApp(ctx context.Context, id string) error {
	if _, err := r.db.ExecContext(ctx,
		`UPDATE applications SET github_app_id = NULL, github_installation_id = 0, auto_deploy = FALSE
		  WHERE github_app_id = ?`, id); err != nil {
		return err
	}
	res, err := r.db.ExecContext(ctx, `DELETE FROM github_apps WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return rowsAffected(res)
}

// --- installations ---

const githubInstallationColumns = `id, github_app_id, installation_id, account_login,
                                   account_type, repository_selection, created_at, updated_at`

func scanGitHubInstallation(scanner interface{ Scan(...any) error }) (*GitHubInstallation, error) {
	var i GitHubInstallation
	var createdAt, updatedAt string
	err := scanner.Scan(&i.ID, &i.GitHubAppID, &i.InstallationID, &i.AccountLogin,
		&i.AccountType, &i.RepositorySelection, &createdAt, &updatedAt, &i.AppName)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, err
	}
	i.CreatedAt, i.UpdatedAt = Parse(createdAt), Parse(updatedAt)
	return &i, nil
}

const githubInstallationSelect = `SELECT i.id, i.github_app_id, i.installation_id, i.account_login,
        i.account_type, i.repository_selection, i.created_at, i.updated_at, a.name
   FROM github_installations i JOIN github_apps a ON a.id = i.github_app_id`

// UpsertGitHubInstallation records an installation, refreshing account info.
func (r *GitHubRepo) UpsertGitHubInstallation(ctx context.Context, i *GitHubInstallation) error {
	if i.ID == "" {
		i.ID = NewID()
	}
	now := Text(time.Now().UTC())
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO github_installations (`+githubInstallationColumns+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (github_app_id, installation_id)
		 DO UPDATE SET account_login = excluded.account_login, account_type = excluded.account_type,
		               repository_selection = excluded.repository_selection, updated_at = excluded.updated_at`,
		i.ID, i.GitHubAppID, i.InstallationID, i.AccountLogin, i.AccountType, i.RepositorySelection, now, now)
	if err != nil {
		return fmt.Errorf("upsert github installation: %w", err)
	}
	i.CreatedAt, i.UpdatedAt = Parse(now), Parse(now)
	return nil
}

// ListGitHubInstallations returns every installation across all apps.
func (r *GitHubRepo) ListGitHubInstallations(ctx context.Context) ([]*GitHubInstallation, error) {
	rows, err := r.db.QueryContext(ctx,
		githubInstallationSelect+` ORDER BY i.account_login ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*GitHubInstallation
	for rows.Next() {
		i, err := scanGitHubInstallation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// GitHubInstallationByInstallationID resolves an installation by GitHub's id.
func (r *GitHubRepo) GitHubInstallationByInstallationID(ctx context.Context, installationID int64) (*GitHubInstallation, error) {
	i, err := scanGitHubInstallation(r.db.QueryRowContext(ctx,
		githubInstallationSelect+` WHERE i.installation_id = ? LIMIT 1`, installationID))
	if err != nil {
		return nil, fmt.Errorf("installation %d: %w", installationID, err)
	}
	return i, nil
}

// DeleteGitHubInstallation removes an installation, e.g. after GitHub reports
// the user uninstalled the app.
func (r *GitHubRepo) DeleteGitHubInstallation(ctx context.Context, installationID int64) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM github_installations WHERE installation_id = ?`, installationID)
	return err
}
