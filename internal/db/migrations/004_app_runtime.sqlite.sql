-- 004_app_runtime: per-application runtime settings from the Coolify-style
-- Networking and Runtime cards: published port mappings, container CPU/memory
-- ceilings, and pre/post-deployment commands.

ALTER TABLE applications ADD COLUMN port_mappings TEXT NOT NULL DEFAULT '';
ALTER TABLE applications ADD COLUMN cpu_limit REAL NOT NULL DEFAULT 0;
ALTER TABLE applications ADD COLUMN memory_limit INTEGER NOT NULL DEFAULT 0;
ALTER TABLE applications ADD COLUMN pre_deploy_command TEXT NOT NULL DEFAULT '';
ALTER TABLE applications ADD COLUMN post_deploy_command TEXT NOT NULL DEFAULT '';
