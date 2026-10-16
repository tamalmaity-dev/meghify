-- 001_init: core control-plane schema.
--
-- Design notes:
--   * Primary keys are TEXT UUIDs generated in Go, so the schema is identical
--     across sqlite and postgres and no sequences are needed.
--   * Booleans are INTEGER (0/1) on sqlite and BOOLEAN on postgres.
--   * Timestamps are TEXT ISO-8601 on sqlite and TIMESTAMPTZ on postgres.

CREATE TABLE users (
    id              TEXT PRIMARY KEY,
    email           TEXT NOT NULL UNIQUE,
    password_hash   TEXT NOT NULL,
    name            TEXT NOT NULL DEFAULT '',
    role            TEXT NOT NULL DEFAULT 'member',
    totp_secret     TEXT NOT NULL DEFAULT '',
    totp_enabled    INTEGER NOT NULL DEFAULT 0,
    totp_confirmed  INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);

CREATE TABLE sessions (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  TEXT NOT NULL UNIQUE,
    ip          TEXT NOT NULL DEFAULT '',
    user_agent  TEXT NOT NULL DEFAULT '',
    expires_at  TEXT NOT NULL,
    created_at  TEXT NOT NULL
);
CREATE INDEX idx_sessions_user ON sessions(user_id);
CREATE INDEX idx_sessions_expiry ON sessions(expires_at);

CREATE TABLE api_tokens (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE,
    prefix       TEXT NOT NULL,
    scopes       TEXT NOT NULL DEFAULT '[]',
    expires_at   TEXT,
    last_used_at TEXT,
    created_at   TEXT NOT NULL
);
CREATE INDEX idx_api_tokens_user ON api_tokens(user_id);

CREATE TABLE ssh_keys (
    id             TEXT PRIMARY KEY,
    name           TEXT NOT NULL,
    public_key     TEXT NOT NULL,
    private_key_enc TEXT NOT NULL,
    fingerprint     TEXT NOT NULL,
    created_at     TEXT NOT NULL
);

CREATE TABLE nodes (
    id             TEXT PRIMARY KEY,
    name           TEXT NOT NULL,
    address        TEXT NOT NULL,
    ssh_port       INTEGER NOT NULL DEFAULT 22,
    ssh_user       TEXT NOT NULL DEFAULT 'root',
    ssh_key_id     TEXT REFERENCES ssh_keys(id) ON DELETE SET NULL,
    status         TEXT NOT NULL DEFAULT 'pending',
    os             TEXT NOT NULL DEFAULT '',
    kernel         TEXT NOT NULL DEFAULT '',
    cpus           INTEGER NOT NULL DEFAULT 0,
    memory_bytes   INTEGER NOT NULL DEFAULT 0,
    docker_version TEXT NOT NULL DEFAULT '',
    last_seen_at   TEXT,
    error_message  TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL
);

CREATE TABLE projects (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE TABLE environments (
    id         TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (project_id, name)
);
CREATE INDEX idx_environments_project ON environments(project_id);

CREATE TABLE applications (
    id             TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    node_id        TEXT REFERENCES nodes(id) ON DELETE SET NULL,
    name           TEXT NOT NULL,
    description    TEXT NOT NULL DEFAULT '',
    git_repository TEXT NOT NULL DEFAULT '',
    git_branch     TEXT NOT NULL DEFAULT 'main',
    build_pack     TEXT NOT NULL DEFAULT 'dockerfile',
    dockerfile     TEXT NOT NULL DEFAULT 'Dockerfile',
    base_directory TEXT NOT NULL DEFAULT '/',
    port          INTEGER NOT NULL DEFAULT 3000,
    health_check   TEXT NOT NULL DEFAULT '/',
    status         TEXT NOT NULL DEFAULT 'idle',
    fqdn           TEXT NOT NULL DEFAULT '',
    image_tag      TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL,
    UNIQUE (environment_id, name)
);
CREATE INDEX idx_applications_env ON applications(environment_id);

CREATE TABLE services (
    id             TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    node_id        TEXT REFERENCES nodes(id) ON DELETE SET NULL,
    name           TEXT NOT NULL,
    kind           TEXT NOT NULL DEFAULT 'compose',
    compose        TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL DEFAULT 'idle',
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL,
    UNIQUE (environment_id, name)
);

CREATE TABLE databases (
    id             TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    node_id        TEXT REFERENCES nodes(id) ON DELETE SET NULL,
    name           TEXT NOT NULL,
    kind           TEXT NOT NULL,
    version        TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL DEFAULT 'idle',
    public_port    INTEGER NOT NULL DEFAULT 0,
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL,
    UNIQUE (environment_id, name)
);

CREATE TABLE deployments (
    id             TEXT PRIMARY KEY,
    application_id TEXT NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    status         TEXT NOT NULL DEFAULT 'queued',
    trigger        TEXT NOT NULL DEFAULT 'manual',
    commit_sha     TEXT NOT NULL DEFAULT '',
    commit_message TEXT NOT NULL DEFAULT '',
    image_tag      TEXT NOT NULL DEFAULT '',
    log_path       TEXT NOT NULL DEFAULT '',
    error          TEXT NOT NULL DEFAULT '',
    queued_at      TEXT NOT NULL,
    started_at     TEXT,
    finished_at    TEXT
);
CREATE INDEX idx_deployments_app ON deployments(application_id, queued_at DESC);

CREATE TABLE env_vars (
    id         TEXT PRIMARY KEY,
    target     TEXT NOT NULL,
    target_id  TEXT NOT NULL,
    key        TEXT NOT NULL,
    value_enc  TEXT NOT NULL DEFAULT '',
    is_secret  INTEGER NOT NULL DEFAULT 0,
    is_build   INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    UNIQUE (target, target_id, key, is_build)
);
CREATE INDEX idx_env_vars_target ON env_vars(target, target_id);

CREATE TABLE domains (
    id             TEXT PRIMARY KEY,
    application_id TEXT NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    host           TEXT NOT NULL UNIQUE,
    tls_enabled    INTEGER NOT NULL DEFAULT 1,
    cert_status    TEXT NOT NULL DEFAULT 'pending',
    created_at     TEXT NOT NULL
);

CREATE TABLE volumes (
    id           TEXT PRIMARY KEY,
    target       TEXT NOT NULL,
    target_id    TEXT NOT NULL,
    name         TEXT NOT NULL,
    host_path    TEXT NOT NULL DEFAULT '',
    mount_path   TEXT NOT NULL,
    read_only    INTEGER NOT NULL DEFAULT 0,
    created_at   TEXT NOT NULL
);
CREATE INDEX idx_volumes_target ON volumes(target, target_id);

-- Durable job queue. Replaces Redis + Horizon entirely.
CREATE TABLE jobs (
    id           TEXT PRIMARY KEY,
    kind         TEXT NOT NULL,
    payload      TEXT NOT NULL DEFAULT '{}',
    status       TEXT NOT NULL DEFAULT 'pending',
    attempts     INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 3,
    run_at       TEXT NOT NULL,
    locked_at    TEXT,
    last_error   TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    started_at   TEXT,
    finished_at  TEXT
);
CREATE INDEX idx_jobs_claimable ON jobs(status, run_at);

CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL
);

CREATE TABLE audit_logs (
    id          TEXT PRIMARY KEY,
    user_id     TEXT,
    action      TEXT NOT NULL,
    entity_type TEXT NOT NULL DEFAULT '',
    entity_id   TEXT NOT NULL DEFAULT '',
    metadata    TEXT NOT NULL DEFAULT '{}',
    ip          TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL
);
CREATE INDEX idx_audit_created ON audit_logs(created_at DESC);

CREATE TABLE node_metrics (
    id          TEXT PRIMARY KEY,
    node_id     TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    cpu_percent REAL NOT NULL DEFAULT 0,
    mem_used    INTEGER NOT NULL DEFAULT 0,
    mem_total   INTEGER NOT NULL DEFAULT 0,
    disk_used   INTEGER NOT NULL DEFAULT 0,
    disk_total  INTEGER NOT NULL DEFAULT 0,
    net_rx      INTEGER NOT NULL DEFAULT 0,
    net_tx      INTEGER NOT NULL DEFAULT 0,
    recorded_at TEXT NOT NULL
);
CREATE INDEX idx_metrics_node_time ON node_metrics(node_id, recorded_at DESC);

CREATE TABLE backups (
    id           TEXT PRIMARY KEY,
    target       TEXT NOT NULL,
    target_id    TEXT NOT NULL,
    destination  TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT 'pending',
    size_bytes   INTEGER NOT NULL DEFAULT 0,
    error        TEXT NOT NULL DEFAULT '',
    schedule     TEXT NOT NULL DEFAULT '',
    last_run_at  TEXT,
    created_at   TEXT NOT NULL
);
CREATE INDEX idx_backups_target ON backups(target, target_id);

CREATE TABLE webhooks (
    id             TEXT PRIMARY KEY,
    application_id TEXT NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    provider       TEXT NOT NULL DEFAULT 'github',
    secret         TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL
);
