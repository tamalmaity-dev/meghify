-- 007_docker_cleanup: server-scoped Docker cleanup schedule, options and
-- execution history. One row configures the panel's own engine; the runs
-- table powers the "Recent executions" list on the server page.

CREATE TABLE docker_cleanup_settings (
    id                 TEXT PRIMARY KEY,
    enabled            INTEGER NOT NULL DEFAULT 1,
    schedule           TEXT NOT NULL DEFAULT '0 0 * * *',
    trigger            TEXT NOT NULL DEFAULT 'schedule',
    threshold_percent  INTEGER NOT NULL DEFAULT 80,
    unused_volumes     INTEGER NOT NULL DEFAULT 0,
    unused_networks    INTEGER NOT NULL DEFAULT 0,
    application_images TEXT NOT NULL DEFAULT 'keep',
    updated_at         TEXT NOT NULL DEFAULT ''
);

CREATE TABLE docker_cleanup_runs (
    id                  TEXT PRIMARY KEY,
    trigger             TEXT NOT NULL,
    status              TEXT NOT NULL,
    started_at          TEXT NOT NULL,
    finished_at         TEXT,
    duration_ms         INTEGER NOT NULL DEFAULT 0,
    reclaimed_bytes     INTEGER NOT NULL DEFAULT 0,
    images_removed      INTEGER NOT NULL DEFAULT 0,
    orphaned_containers INTEGER NOT NULL DEFAULT 0,
    volumes_removed     INTEGER NOT NULL DEFAULT 0,
    networks_removed    INTEGER NOT NULL DEFAULT 0,
    error               TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_docker_cleanup_runs_started ON docker_cleanup_runs(started_at DESC);
