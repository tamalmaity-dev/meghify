-- 002_github: GitHub App sources, their installations, and the link from an
-- application to the repository it deploys from (postgres dialect).
--
-- Mirrors 002_github.sqlite.sql. Booleans are BOOLEAN and timestamps are
-- TIMESTAMPTZ.

CREATE TABLE github_apps (
    id                 TEXT PRIMARY KEY,
    name               TEXT NOT NULL,
    slug               TEXT NOT NULL DEFAULT '',
    app_id             INTEGER NOT NULL DEFAULT 0,
    client_id          TEXT NOT NULL DEFAULT '',
    client_secret_enc  TEXT NOT NULL DEFAULT '',
    private_key_enc    TEXT NOT NULL DEFAULT '',
    webhook_secret_enc TEXT NOT NULL DEFAULT '',
    html_url           TEXT NOT NULL DEFAULT '',
    base_url           TEXT NOT NULL DEFAULT '',
    organization       TEXT NOT NULL DEFAULT '',
    state              TEXT NOT NULL DEFAULT '',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE github_installations (
    id                   TEXT PRIMARY KEY,
    github_app_id        TEXT NOT NULL REFERENCES github_apps(id) ON DELETE CASCADE,
    installation_id      INTEGER NOT NULL,
    account_login        TEXT NOT NULL DEFAULT '',
    account_type         TEXT NOT NULL DEFAULT '',
    repository_selection TEXT NOT NULL DEFAULT '',
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (github_app_id, installation_id)
);
CREATE INDEX idx_github_installations_app ON github_installations(github_app_id);

ALTER TABLE applications ADD COLUMN github_app_id TEXT REFERENCES github_apps(id) ON DELETE SET NULL;
ALTER TABLE applications ADD COLUMN github_installation_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE applications ADD COLUMN repository_full_name TEXT NOT NULL DEFAULT '';
ALTER TABLE applications ADD COLUMN auto_deploy BOOLEAN NOT NULL DEFAULT FALSE;
