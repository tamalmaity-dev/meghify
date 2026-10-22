-- 009_domain_www: canonical www/apex redirect per domain (postgres dialect).
-- Mirrors 009_domain_www.sqlite.sql.

ALTER TABLE domains ADD COLUMN www_redirect TEXT NOT NULL DEFAULT '';
