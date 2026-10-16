package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/tamalmaity-dev/meghify/internal/db"
	"github.com/tamalmaity-dev/meghify/internal/node"
)

type nodeRequest struct {
	Name     string `json:"name"`
	Address  string `json:"address"`
	SSHPort  int    `json:"sshPort"`
	SSHUser  string `json:"sshUser"`
	SSHKeyID string `json:"sshKeyId"`
}

func (s *Server) listNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.store.Nodes.ListNodes(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, nodes)
}

func (s *Server) getNode(w http.ResponseWriter, r *http.Request) {
	n, err := s.store.Nodes.NodeByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) createNode(w http.ResponseWriter, r *http.Request) {
	var req nodeRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "node name is required")
		return
	}
	if strings.TrimSpace(req.Address) == "" {
		writeError(w, http.StatusBadRequest, "node address is required")
		return
	}

	port := req.SSHPort
	if port <= 0 {
		port = 22
	}
	user := req.SSHUser
	if user == "" {
		user = "root"
	}

	n := &db.Node{
		ID:       db.NewID(),
		Name:     strings.TrimSpace(req.Name),
		Address:  strings.TrimSpace(req.Address),
		SSHPort:  port,
		SSHUser:  user,
		SSHKeyID: req.SSHKeyID,
		Status:   db.NodePending,
	}
	if err := s.store.Nodes.CreateNode(r.Context(), n); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r.Context(), r, "node.create", "node", n.ID, nil)
	writeJSON(w, http.StatusCreated, n)
}

func (s *Server) deleteNode(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if s.nodes != nil {
		s.nodes.Forget(id)
	}
	if err := s.store.Nodes.DeleteNode(r.Context(), id); err != nil {
		mapError(w, err)
		return
	}
	s.audit(r.Context(), r, "node.delete", "node", id, nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// provisionNode installs Docker on the node and opens a tunnel to its engine.
func (s *Server) provisionNode(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	n, err := s.store.Nodes.NodeByID(r.Context(), id)
	if err != nil {
		mapError(w, err)
		return
	}
	if s.nodes == nil {
		writeError(w, http.StatusServiceUnavailable, "node support is not enabled")
		return
	}

	go s.provisionAsync(n)
	writeJSON(w, http.StatusAccepted, map[string]string{"status": db.NodeInstall})
}

func (s *Server) provisionAsync(n *db.Node) {
	ctx, cancel := contextWithTimeout(5 * time.Minute)
	defer cancel()

	s.store.Nodes.PatchNodeState(ctx, n.ID, db.NodeInstall, "", "", 0, 0, "", "", time.Now().UTC())

	var progress []string
	collect := func(line string) {
		progress = append(progress, line)
		if s.hub != nil {
			s.hub.BroadcastApplication("node:"+n.ID, map[string]any{
				"nodeId": n.ID,
				"event":  "provision",
				"line":   line,
			})
		}
	}

	if err := s.nodes.Install(ctx, n, collect); err != nil {
		s.store.Nodes.PatchNodeState(ctx, n.ID, db.NodeError, "", "", 0, 0, "", err.Error(), time.Now().UTC())
		return
	}

	// A successful install means the engine is reachable through the tunnel.
	if s.nodes.Reachable(ctx, n) {
		s.store.Nodes.PatchNodeState(ctx, n.ID, db.NodeOnline, "", "", 0, 0, "", "", time.Now().UTC())
		if s.hub != nil {
			s.hub.BroadcastApplication("node:"+n.ID, map[string]any{"nodeId": n.ID, "event": "online"})
		}
		return
	}

	s.store.Nodes.PatchNodeState(ctx, n.ID, db.NodeError, "", "", 0, 0, "",
		"docker engine unreachable after install", time.Now().UTC())
}

// nodeMetrics returns historical resource samples.
func (s *Server) nodeMetrics(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	since := time.Now().UTC().Add(-24 * time.Hour)
	if raw := r.URL.Query().Get("since"); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil {
			since = time.Now().UTC().Add(-parsed)
		}
	}
	metrics, err := s.store.Nodes.Metrics(r.Context(), id, since, 500)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if metrics == nil {
		metrics = []*db.NodeMetrics{}
	}
	writeJSON(w, http.StatusOK, metrics)
}

// nodeContainers lists the managed containers on a node.
func (s *Server) nodeContainers(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	client, err := s.clientForNode(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	containers, err := client.ListContainers(r.Context(), "", "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if containers == nil {
		containers = []*dockerContainerInfo{}
	}
	writeJSON(w, http.StatusOK, containers)
}

// --- SSH keys ---

func (s *Server) listSSHKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.store.Nodes.ListSSHKeys(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, keys)
}

func (s *Server) createSSHKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	pair, err := node.GenerateKeyPair()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// The private half is encrypted at rest with the instance secret.
	sealed, err := s.auth.Seal(pair.PrivatePEM)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	key := &db.SSHKey{
		ID:            db.NewID(),
		Name:          valueOr(req.Name, "node-key"),
		PublicKey:     pair.PublicLine,
		PrivateKeyEnc: sealed,
		Fingerprint:   pair.Fingerprint,
	}
	if err := s.store.Nodes.CreateSSHKey(r.Context(), key); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r.Context(), r, "ssh_key.create", "ssh_key", key.ID, nil)
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":          key.ID,
		"name":        key.Name,
		"publicKey":   key.PublicKey,
		"fingerprint": key.Fingerprint,
	})
}

func (s *Server) deleteSSHKey(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.store.Nodes.DeleteSSHKey(r.Context(), id); err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
