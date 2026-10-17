-- 005_domains: per-domain routing options (postgres dialect).
--
-- Mirrors 005_domains.sqlite.sql. Flags are BOOLEAN, timestamps TIMESTAMPTZ.

ALTER TABLE domains ADD COLUMN path TEXT NOT NULL DEFAULT '';
ALTER TABLE domains ADD COLUMN port INTEGER NOT NULL DEFAULT 0;
ALTER TABLE domains ADD COLUMN redirect_https BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE domains ADD COLUMN search_indexing BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE domains ADD COLUMN dns_status TEXT NOT NULL DEFAULT '';
ALTER TABLE domains ADD COLUMN dns_checked_at TIMESTAMPTZ;

ALTER TABLE applications ADD COLUMN redirect_https BOOLEAN NOT NULL DEFAULT TRUE;
