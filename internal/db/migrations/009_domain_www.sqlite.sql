-- 009_domain_www: canonical www/apex redirect per domain.
--
-- Empty keeps the historical exact-host route. "www" redirects the apex to
-- www.<host>; "non-www" redirects www.<host> to the apex. Both hostnames are
-- included in the router so a certificate covers both.

ALTER TABLE domains ADD COLUMN www_redirect TEXT NOT NULL DEFAULT '';
