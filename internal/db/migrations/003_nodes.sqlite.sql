-- 003_nodes: server detail facts. The Server overview card shows the same
-- facts Coolify does, and the Connection card lets the operator keep a note
-- next to the server. The singleton 'local' row (the machine the panel runs
-- on) is flagged with a description so the UI - and the delete guard - can
-- treat it as the default server.

ALTER TABLE nodes ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN arch TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN boot_time TEXT;

UPDATE nodes
   SET description = 'This is the server where Meghify is running on. Don''t remove it!'
 WHERE id = 'local' AND description = '';
