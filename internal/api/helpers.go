package api

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/tamalmaity-dev/meghify/internal/db"
	"github.com/tamalmaity-dev/meghify/internal/docker"
)

// errNoEngine is the domain error for an instance configured without Docker
// access. Handlers map it to 503 instead of leaking an internal nil pointer.
var errNoEngine = errors.New("docker engine is not configured")

// validateNodeSelection accepts the panel's own node and any registered remote
// node. Storing an arbitrary string used to produce applications that could
// never deploy and obscured the difference between local and remote.
func (s *Server) validateNodeSelection(ctx context.Context, nodeID string) error {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" || nodeID == "local" || nodeID == db.LocalNodeID {
		return nil
	}
	if _, err := s.store.Nodes.NodeByID(ctx, nodeID); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return errors.New("selected server does not exist")
		}
		return err
	}
	return nil
}

// dockerContainerInfo is exposed so the API package does not need to import the
// docker types it merely passes through.
type dockerContainerInfo = docker.ContainerInfo

// contextWithTimeout builds a cancellable context for background work.
func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// clientForNode resolves the engine client for a node id. The special id "local"
// (or an empty id) means the panel's own server.
func (s *Server) clientForNode(ctx context.Context, nodeID string) (*docker.Client, error) {
	if s.nodes == nil {
		if s.docker == nil {
			return nil, errNoEngine
		}
		return s.docker, nil
	}
	if nodeID == "" || nodeID == "local" {
		return s.nodes.ClientFor(ctx, nil)
	}

	n, err := s.store.Nodes.NodeByID(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	return s.nodes.ClientFor(ctx, n)
}

// nodeFor resolves a node record, allowing nil for the local server.
func (s *Server) nodeFor(ctx context.Context, nodeID string) (*db.Node, error) {
	if nodeID == "" || nodeID == "local" {
		return nil, nil
	}
	return s.store.Nodes.NodeByID(ctx, nodeID)
}
