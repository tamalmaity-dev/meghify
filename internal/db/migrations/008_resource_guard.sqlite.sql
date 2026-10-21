-- 008_resource_guard: panel-wide resource protection for application
-- containers, per-application overrides, and a durable action history.
--
-- The guard samples application containers on every managed engine. CPU is
-- measured as a percentage of total host capacity; memory is measured against
-- the container limit when one is configured, otherwise against host RAM.

CREATE TABLE resource_guard_settings (
    id                           TEXT PRIMARY KEY,
    enabled                      INTEGER NOT NULL DEFAULT 1,
    cpu_enabled                  INTEGER NOT NULL DEFAULT 1,
    cpu_percent                  INTEGER NOT NULL DEFAULT 90,
    cpu_duration_seconds         INTEGER NOT NULL DEFAULT 600,
    cpu_action                   TEXT NOT NULL DEFAULT 'throttle',
    cpu_throttle_percent         INTEGER NOT NULL DEFAULT 50,
    memory_enabled               INTEGER NOT NULL DEFAULT 1,
    memory_percent               INTEGER NOT NULL DEFAULT 90,
    memory_duration_seconds      INTEGER NOT NULL DEFAULT 300,
    memory_action                TEXT NOT NULL DEFAULT 'stop',
    host_memory_enabled          INTEGER NOT NULL DEFAULT 1,
    host_memory_percent          INTEGER NOT NULL DEFAULT 95,
    host_memory_duration_seconds INTEGER NOT NULL DEFAULT 120,
    grace_seconds                INTEGER NOT NULL DEFAULT 300,
    cooldown_seconds             INTEGER NOT NULL DEFAULT 900,
    updated_at                   TEXT NOT NULL DEFAULT ''
);

CREATE TABLE resource_guard_events (
    id                TEXT PRIMARY KEY,
    application_id    TEXT NOT NULL DEFAULT '',
    application_name  TEXT NOT NULL DEFAULT '',
    node_id           TEXT NOT NULL DEFAULT '',
    container_id      TEXT NOT NULL DEFAULT '',
    container_name    TEXT NOT NULL DEFAULT '',
    metric            TEXT NOT NULL,
    action            TEXT NOT NULL,
    status            TEXT NOT NULL,
    observed_percent  REAL NOT NULL DEFAULT 0,
    threshold_percent INTEGER NOT NULL DEFAULT 0,
    duration_seconds  INTEGER NOT NULL DEFAULT 0,
    usage_bytes       INTEGER NOT NULL DEFAULT 0,
    limit_bytes       INTEGER NOT NULL DEFAULT 0,
    message           TEXT NOT NULL DEFAULT '',
    error             TEXT NOT NULL DEFAULT '',
    created_at        TEXT NOT NULL
);
CREATE INDEX idx_resource_guard_events_created ON resource_guard_events(created_at DESC);
CREATE INDEX idx_resource_guard_events_app ON resource_guard_events(application_id, created_at DESC);

ALTER TABLE applications ADD COLUMN resource_guard_enabled INTEGER NOT NULL DEFAULT 1;
ALTER TABLE applications ADD COLUMN resource_guard_cpu_action TEXT NOT NULL DEFAULT '';
ALTER TABLE applications ADD COLUMN resource_guard_memory_action TEXT NOT NULL DEFAULT '';
ALTER TABLE applications ADD COLUMN resource_guard_cpu_percent INTEGER NOT NULL DEFAULT 0;
ALTER TABLE applications ADD COLUMN resource_guard_memory_percent INTEGER NOT NULL DEFAULT 0;
