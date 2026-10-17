-- 004_app_runtime: per-application runtime settings (postgres dialect).
--
-- Mirrors 004_app_runtime.sqlite.sql.

ALTER TABLE applications ADD COLUMN port_mappings TEXT NOT NULL DEFAULT '';
ALTER TABLE applications ADD COLUMN cpu_limit DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE applications ADD COLUMN memory_limit BIGINT NOT NULL DEFAULT 0;
ALTER TABLE applications ADD COLUMN pre_deploy_command TEXT NOT NULL DEFAULT '';
ALTER TABLE applications ADD COLUMN post_deploy_command TEXT NOT NULL DEFAULT '';
