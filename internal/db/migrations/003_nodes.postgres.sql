-- 003_nodes: server detail facts (postgres dialect).
--
-- Mirrors 003_nodes.sqlite.sql. Timestamps are TIMESTAMPTZ.

ALTER TABLE nodes ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN arch TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN boot_time TIMESTAMPTZ;

UPDATE nodes
   SET description = 'This is the server where Meghify is running on. Don''t remove it!'
 WHERE id = 'local' AND description = '';
