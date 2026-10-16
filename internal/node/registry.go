package node

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/tamalmaity-dev/meghify/internal/auth"
	"github.com/tamalmaity-dev/meghify/internal/db"
	"github.com/tamalmaity-dev/meghify/internal/docker"
)

// Registry owns one SSH dialer, tunnel and engine client per node.
//
// Connections are lazily created and dropped when a node is deleted, so an
// unreachable node costs nothing more than a row in the database.
type Registry struct {
	secrets *auth.SecretBox
	local   *docker.Client

	mu     sync.Mutex
	states map[string]*nodeState
	keySrc keySourceFunc
}

type nodeState struct {
	dialer    *Dialer
	tunnel    *Tunnel
	client    *docker.Client
	localDial bool
}

// NewRegistry builds the node registry. local is the client for the machine the
// panel itself runs on, and may be nil when no engine is available there.
func NewRegistry(secrets *auth.SecretBox, local *docker.Client) *Registry {
	return &Registry{
		secrets: secrets,
		local:   local,
		states:  map[string]*nodeState{},
	}
}

// ClientFor returns an engine client for a node. A nil node means "this server".
func (r *Registry) ClientFor(ctx context.Context, n *db.Node) (*docker.Client, error) {
	if n == nil || n.Address == "" {
		if r.local == nil {
			return nil, fmt.Errorf("no local docker engine configured")
		}
		return r.local, nil
	}

	r.mu.Lock()
	state, ok := r.states[n.ID]
	r.mu.Unlock()
	if ok && state.client != nil {
		if state.localDial || pingOK(ctx, state.client) {
			return state.client, nil
		}
		r.Forget(n.ID)
	}

	return r.connect(ctx, n)
}

func (r *Registry) connect(ctx context.Context, n *db.Node) (*docker.Client, error) {
	if n.SSHKeyID == "" {
		return nil, fmt.Errorf("node %s has no ssh key assigned", n.Name)
	}

	key, err := r.sshKey(ctx, n)
	if err != nil {
		return nil, err
	}

	dialer, err := NewDialer(n.Address, n.SSHPort, n.SSHUser, key)
	if err != nil {
		return nil, err
	}

	tunnel, err := OpenTunnel(dialer, 2376)
	if err != nil {
		return nil, err
	}

	client, err := NewDockerClient(tunnel.LocalAddr())
	if err != nil {
		tunnel.Close()
		return nil, err
	}
	if err := client.Ping(ctx); err != nil {
		tunnel.Close()
		return nil, fmt.Errorf("docker engine on %s unreachable: %w", n.Name, err)
	}

	r.mu.Lock()
	r.states[n.ID] = &nodeState{dialer: dialer, tunnel: tunnel, client: client}
	r.mu.Unlock()
	return client, nil
}

func (r *Registry) sshKey(ctx context.Context, n *db.Node) (string, error) {
	if r.secrets == nil {
		return "", fmt.Errorf("secret box is not configured")
	}
	if n.SSHKeyID == "" {
		return "", fmt.Errorf("node %s has no ssh key", n.Name)
	}
	source := r.keySrc
	if source == nil {
		source = keySource
	}
	if source == nil {
		return "", fmt.Errorf("ssh key lookup is not configured")
	}
	sealed, err := source(ctx, n.SSHKeyID)
	if err != nil {
		return "", err
	}
	private, err := r.secrets.Decrypt(sealed)
	if err != nil {
		return "", fmt.Errorf("decrypt ssh key: %w", err)
	}
	return private, nil
}

// keySource resolves the encrypted private key for a stored ssh key id.
type keySourceFunc func(ctx context.Context, id string) (string, error)

// keySource is populated by the API layer at startup so the registry can look
// up key material without depending on the database package directly.
var keySource keySourceFunc

// SetKeySource wires the database lookup used to resolve ssh key material.
func SetKeySource(fn keySourceFunc) { keySource = fn }

// Install provisions Docker on a node, streaming progress to sink.
func (r *Registry) Install(ctx context.Context, n *db.Node, sink func(string)) error {
	if n.SSHKeyID == "" {
		return fmt.Errorf("node %s has no ssh key assigned", n.Name)
	}
	key, err := r.sshKey(ctx, n)
	if err != nil {
		return err
	}

	dialer, err := NewDialer(n.Address, n.SSHPort, n.SSHUser, key)
	if err != nil {
		return err
	}

	provisioner := NewProvisioner(dialer)
	provisioner.OnProgress(sink)
	installCtx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	return provisioner.Install(installCtx)
}

// Reachable reports whether the node's engine answers through a tunnel.
func (r *Registry) Reachable(ctx context.Context, n *db.Node) bool {
	client, err := r.ClientFor(ctx, n)
	if err != nil {
		return false
	}
	return client.Ping(ctx) == nil
}

// Forget tears down every connection for a node.
func (r *Registry) Forget(nodeID string) {
	r.mu.Lock()
	state, ok := r.states[nodeID]
	if ok {
		delete(r.states, nodeID)
	}
	r.mu.Unlock()

	if !ok || state == nil {
		return
	}
	if state.tunnel != nil {
		state.tunnel.Close()
	}
	if state.dialer != nil {
		state.dialer.Close()
	}
	if state.client != nil {
		state.client.Close()
	}
}

// Close drops every node connection.
func (r *Registry) Close() {
	r.mu.Lock()
	states := r.states
	r.states = map[string]*nodeState{}
	r.mu.Unlock()

	for _, state := range states {
		if state.tunnel != nil {
			state.tunnel.Close()
		}
		if state.dialer != nil {
			state.dialer.Close()
		}
		if state.client != nil {
			state.client.Close()
		}
	}
}

func pingOK(ctx context.Context, client *docker.Client) bool {
	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return client.Ping(pingCtx) == nil
}
