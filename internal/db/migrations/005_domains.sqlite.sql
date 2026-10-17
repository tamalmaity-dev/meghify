-- 005_domains: per-domain routing options from the Coolify-style Domains
-- card — protocol (tls_enabled already exists), internal port override, path
-- prefix, HTTP→HTTPS redirect, search-indexing toggle, and the cached DNS
-- check result. The application-level redirect_https is the default for its
-- domains and feeds label generation.

ALTER TABLE domains ADD COLUMN path TEXT NOT NULL DEFAULT '';
ALTER TABLE domains ADD COLUMN port INTEGER NOT NULL DEFAULT 0;
ALTER TABLE domains ADD COLUMN redirect_https INTEGER NOT NULL DEFAULT 1;
ALTER TABLE domains ADD COLUMN search_indexing INTEGER NOT NULL DEFAULT 1;
ALTER TABLE domains ADD COLUMN dns_status TEXT NOT NULL DEFAULT '';
ALTER TABLE domains ADD COLUMN dns_checked_at TEXT;

ALTER TABLE applications ADD COLUMN redirect_https INTEGER NOT NULL DEFAULT 1;
