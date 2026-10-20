-- 006_node_host_key: pin the SSH host key of a remote node.
--
-- Empty means "trust on first use": the panel records the key it sees on the
-- next successful connection. Once set, any different key is rejected, which
-- turns a mis-issued certificate or an on-path attacker into a clear error
-- instead of silently forwarding the node's Docker socket credentials.
ALTER TABLE nodes ADD COLUMN host_key_fingerprint TEXT NOT NULL DEFAULT '';
