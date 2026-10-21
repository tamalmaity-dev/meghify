-- 007_docker_cleanup: server-scoped Docker cleanup schedule, options and
-- execution history. One row configures the panel's own engine; the runs
-- table powers the "Recent executions" list on the server page.

CREATE TABLE docker_cleanup_settings (
    id                 TEXT PRIMARY KEY,
    enabled            BOOLEAN NOT NULL DEFAULT TRUE,
    schedule           TEXT NOT NULL DEFAULT '0 0 * * *',
    trigger            TEXT NOT NULL DEFAULT 'schedule',
    threshold_percent  INTEGER NOT NULL DEFAULT 80,
    unused_volumes     BOOLEAN NOT NULL DEFAULT FALSE,
    unused_networks    BOOLEAN NOT NULL DEFAULT FALSE,
    application_images TEXT NOT NULL DEFAULT 'keep',
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE docker_cleanup_runs (
    id                  TEXT PRIMARY KEY,
    trigger             TEXT NOT NULL,
    status              TEXT NOT NULL,
    started_at          TIMESTAMPTZ NOT NULL,
    finished_at         TIMESTAMPTZ,
    duration_ms         BIGINT NOT NULL DEFAULT 0,
    reclaimed_bytes     BIGINT NOT NULL DEFAULT 0,
    images_removed      BIGINT NOT NULL DEFAULT 0,
    orphaned_containers BIGINT NOT NULL DEFAULT 0,
    volumes_removed     BIGINT NOT NULL DEFAULT 0,
    networks_removed    BIGINT NOT NULL DEFAULT 0,
    error               TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_docker_cleanup_runs_started ON docker_cleanup_runs(started_at DESC);
