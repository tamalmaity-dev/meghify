package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// NodeRepo persists servers and their metric samples.
type NodeRepo struct{ db *sql.DB }

// CreateNode inserts a server record in the pending state.
func (r *NodeRepo) CreateNode(ctx context.Context, n *Node) error {
	now := Text(time.Now().UTC())
	q := `INSERT INTO nodes (id, name, address, ssh_port, ssh_user, ssh_key_id, status, os, kernel,
	                         cpus, memory_bytes, docker_version, last_seen_at, error_message, created_at, updated_at)
	      VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if _, err := r.db.ExecContext(ctx, q,
		n.ID, n.Name, n.Address, n.SSHPort, n.SSHUser, NullString(n.SSHKeyID), n.Status, n.OS, n.Kernel,
		n.CPUs, n.MemoryBytes, n.DockerVersion, TextOptional(n.LastSeenAt), n.ErrorMessage, now, now,
	); err != nil {
		return fmt.Errorf("insert node: %w", err)
	}
	n.CreatedAt, n.UpdatedAt = Parse(now), Parse(now)
	return nil
}

// EnsureLocalNode creates the singleton row that local engine metrics are
// recorded against. node_metrics.node_id is foreign-keyed to nodes, so without
// this row a fresh install fails every local sample with a constraint error.
func (r *NodeRepo) EnsureLocalNode(ctx context.Context) error {
	now := Text(time.Now().UTC())
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO nodes (id, name, address, status, created_at, updated_at)
		 VALUES ('local', 'This server', 'localhost', ?, ?, ?)
		 ON CONFLICT (id) DO NOTHING`, NodeOnline, now, now)
	if err != nil {
		return fmt.Errorf("ensure local node: %w", err)
	}
	return nil
}

// UpdateLocalNodeInfo refreshes the discoverable hardware facts of the local
// node so the servers list and dashboard show real numbers instead of empty
// fields. It also marks the row as seen now.
func (r *NodeRepo) UpdateLocalNodeInfo(ctx context.Context, osName, kernel string, cpus int, memoryBytes int64) error {
	now := Text(time.Now().UTC())
	_, err := r.db.ExecContext(ctx,
		`UPDATE nodes SET os = ?, kernel = ?, cpus = ?, memory_bytes = ?, last_seen_at = ?, updated_at = ?
		  WHERE id = 'local'`,
		osName, kernel, cpus, memoryBytes, now, now)
	if err != nil {
		return fmt.Errorf("update local node info: %w", err)
	}
	return nil
}

const nodeColumns = `id, name, address, ssh_port, ssh_user, ssh_key_id, status, os, kernel,
                     cpus, memory_bytes, docker_version, last_seen_at, error_message, created_at, updated_at`

func (r *NodeRepo) scanNode(scanner interface{ Scan(...any) error }) (*Node, error) {
	var n Node
	var sshKeyID, lastSeen, createdAt, updatedAt sql.NullString
	err := scanner.Scan(&n.ID, &n.Name, &n.Address, &n.SSHPort, &n.SSHUser, &sshKeyID,
		&n.Status, &n.OS, &n.Kernel, &n.CPUs, &n.MemoryBytes, &n.DockerVersion,
		&lastSeen, &n.ErrorMessage, &createdAt, &updatedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, err
	}
	n.SSHKeyID = nullString(sshKeyID)
	n.LastSeenAt = optionalTime(lastSeen)
	n.CreatedAt, n.UpdatedAt = Parse(nullString(createdAt)), Parse(nullString(updatedAt))
	return &n, nil
}

// NodeByID loads a node.
func (r *NodeRepo) NodeByID(ctx context.Context, id string) (*Node, error) {
	n, err := r.scanNode(r.db.QueryRowContext(ctx,
		`SELECT `+nodeColumns+` FROM nodes WHERE id = ?`, id))
	if err != nil {
		return nil, fmt.Errorf("node %s: %w", id, err)
	}
	return n, nil
}

// ListNodes returns every node, newest first.
func (r *NodeRepo) ListNodes(ctx context.Context) ([]*Node, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+nodeColumns+` FROM nodes ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Node
	for rows.Next() {
		n, err := r.scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// UpdateNode writes the full mutable node record.
func (r *NodeRepo) UpdateNode(ctx context.Context, n *Node) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE nodes SET name = ?, address = ?, ssh_port = ?, ssh_user = ?, ssh_key_id = ?, status = ?,
		                  os = ?, kernel = ?, cpus = ?, memory_bytes = ?, docker_version = ?,
		                  last_seen_at = ?, error_message = ?, updated_at = ?
		  WHERE id = ?`,
		n.Name, n.Address, n.SSHPort, n.SSHUser, NullString(n.SSHKeyID), n.Status,
		n.OS, n.Kernel, n.CPUs, n.MemoryBytes, n.DockerVersion,
		TextOptional(n.LastSeenAt), n.ErrorMessage, Text(time.Now().UTC()), n.ID)
	if err != nil {
		return err
	}
	return rowsAffected(res)
}

// PatchNodeState updates the reachability fields only.
func (r *NodeRepo) PatchNodeState(ctx context.Context, id, status, osName, kernel string, cpus int, mem int64, dockerVersion, errMsg string, lastSeen time.Time) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE nodes SET status = ?, os = ?, kernel = ?, cpus = ?, memory_bytes = ?, docker_version = ?,
		                  last_seen_at = ?, error_message = ?, updated_at = ?
		  WHERE id = ?`,
		status, osName, kernel, cpus, mem, dockerVersion, Text(lastSeen), errMsg, Text(time.Now().UTC()), id)
	if err != nil {
		return err
	}
	return rowsAffected(res)
}

// DeleteNode removes a node. Resources referencing it are detached, not deleted.
func (r *NodeRepo) DeleteNode(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM nodes WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return rowsAffected(res)
}

// CreateSSHKey stores a key pair with its private half encrypted at rest.
func (r *NodeRepo) CreateSSHKey(ctx context.Context, k *SSHKey) error {
	now := Text(time.Now().UTC())
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO ssh_keys (id, name, public_key, private_key_enc, fingerprint, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		k.ID, k.Name, k.PublicKey, k.PrivateKeyEnc, k.Fingerprint, now); err != nil {
		return fmt.Errorf("insert ssh key: %w", err)
	}
	k.CreatedAt = Parse(now)
	return nil
}

// ListSSHKeys returns every stored key.
func (r *NodeRepo) ListSSHKeys(ctx context.Context) ([]*SSHKey, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, public_key, private_key_enc, fingerprint, created_at FROM ssh_keys ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*SSHKey
	for rows.Next() {
		var k SSHKey
		var createdAt string
		if err := rows.Scan(&k.ID, &k.Name, &k.PublicKey, &k.PrivateKeyEnc, &k.Fingerprint, &createdAt); err != nil {
			return nil, err
		}
		k.CreatedAt = Parse(createdAt)
		out = append(out, &k)
	}
	return out, rows.Err()
}

// SSHKeyByID loads a key.
func (r *NodeRepo) SSHKeyByID(ctx context.Context, id string) (*SSHKey, error) {
	var k SSHKey
	var createdAt string
	err := r.db.QueryRowContext(ctx,
		`SELECT id, name, public_key, private_key_enc, fingerprint, created_at FROM ssh_keys WHERE id = ?`, id).
		Scan(&k.ID, &k.Name, &k.PublicKey, &k.PrivateKeyEnc, &k.Fingerprint, &createdAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, err
	}
	k.CreatedAt = Parse(createdAt)
	return &k, nil
}

// DeleteSSHKey removes a key.
func (r *NodeRepo) DeleteSSHKey(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM ssh_keys WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return rowsAffected(res)
}

// InsertMetric stores a resource sample.
func (r *NodeRepo) InsertMetric(ctx context.Context, m *NodeMetrics) error {
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO node_metrics (id, node_id, cpu_percent, mem_used, mem_total, disk_used, disk_total, net_rx, net_tx, recorded_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.NodeID, m.CPUPercent, m.MemUsed, m.MemTotal, m.DiskUsed, m.DiskTotal,
		m.NetRx, m.NetTx, Text(m.RecordedAt)); err != nil {
		return fmt.Errorf("insert metric: %w", err)
	}
	return nil
}

// Metrics since the cutoff, oldest first, capped at limit rows.
func (r *NodeRepo) Metrics(ctx context.Context, nodeID string, since time.Time, limit int) ([]*NodeMetrics, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, node_id, cpu_percent, mem_used, mem_total, disk_used, disk_total, net_rx, net_tx, recorded_at
		   FROM node_metrics WHERE node_id = ? AND recorded_at >= ? ORDER BY recorded_at ASC LIMIT ?`,
		nodeID, Text(since), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*NodeMetrics
	for rows.Next() {
		var m NodeMetrics
		var recordedAt string
		if err := rows.Scan(&m.ID, &m.NodeID, &m.CPUPercent, &m.MemUsed, &m.MemTotal,
			&m.DiskUsed, &m.DiskTotal, &m.NetRx, &m.NetTx, &recordedAt); err != nil {
			return nil, err
		}
		m.RecordedAt = Parse(recordedAt)
		out = append(out, &m)
	}
	return out, rows.Err()
}

// LatestMetric returns the most recent sample for a node, or nil.
func (r *NodeRepo) LatestMetric(ctx context.Context, nodeID string) (*NodeMetrics, error) {
	var m NodeMetrics
	var recordedAt string
	err := r.db.QueryRowContext(ctx,
		`SELECT id, node_id, cpu_percent, mem_used, mem_total, disk_used, disk_total, net_rx, net_tx, recorded_at
		   FROM node_metrics WHERE node_id = ? ORDER BY recorded_at DESC LIMIT 1`, nodeID).
		Scan(&m.ID, &m.NodeID, &m.CPUPercent, &m.MemUsed, &m.MemTotal,
			&m.DiskUsed, &m.DiskTotal, &m.NetRx, &m.NetTx, &recordedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, err
	}
	m.RecordedAt = Parse(recordedAt)
	return &m, nil
}

// PruneMetrics drops samples older than the retention window.
func (r *NodeRepo) PruneMetrics(ctx context.Context, olderThan time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM node_metrics WHERE recorded_at < ?`, Text(olderThan))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
