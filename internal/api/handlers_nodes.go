package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/tamalmaity-dev/meghify/internal/db"
	"github.com/tamalmaity-dev/meghify/internal/docker"
	"github.com/tamalmaity-dev/meghify/internal/node"
)

type nodeRequest struct {
	Name               string `json:"name"`
	Address            string `json:"address"`
	SSHPort            int    `json:"sshPort"`
	SSHUser            string `json:"sshUser"`
	SSHKeyID           string `json:"sshKeyId"`
	HostKeyFingerprint string `json:"hostKeyFingerprint"`
}

// validateHostKey accepts the OpenSSH SHA256 fingerprint format. Leaving it
// empty is allowed: the first successful connection pins it (TOFU).
func validateHostKey(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if !strings.HasPrefix(value, "SHA256:") || len(value) < 12 || len(value) > 128 {
		return "", errors.New("host key fingerprint must look like SHA256:... (run ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub on the server)")
	}
	return value, nil
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
	fingerprint, err := validateHostKey(req.HostKeyFingerprint)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	n := &db.Node{
		ID:                 db.NewID(),
		Name:               strings.TrimSpace(req.Name),
		Address:            strings.TrimSpace(req.Address),
		SSHPort:            port,
		SSHUser:            user,
		SSHKeyID:           req.SSHKeyID,
		HostKeyFingerprint: fingerprint,
		Status:             db.NodePending,
	}
	if err := s.store.Nodes.CreateNode(r.Context(), n); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r.Context(), r, "node.create", "node", n.ID, nil)
	writeJSON(w, http.StatusCreated, n)
}

// deleteNode removes a server. The default "local" row is the machine the
// panel itself runs on — removing it would break the dashboard and every
// local engine call, so it is refused outright.
func (s *Server) deleteNode(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == db.LocalNodeID {
		writeError(w, http.StatusConflict, "the server running the panel cannot be removed")
		return
	}
	// Never orphan workloads: a removed node cannot deploy, route, stop or
	// remove the containers that still reference it.
	var resources int
	if err := s.store.Handle().QueryRowContext(r.Context(),
		`SELECT
		   (SELECT COUNT(*) FROM applications WHERE node_id = ?) +
		   (SELECT COUNT(*) FROM services WHERE node_id = ?) +
		   (SELECT COUNT(*) FROM databases WHERE node_id = ?)`,
		id, id, id).Scan(&resources); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if resources > 0 {
		writeError(w, http.StatusConflict,
			"this server still hosts applications, services or databases; move or delete them first")
		return
	}
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

type nodeUpdateRequest struct {
	Name               string `json:"name"`
	Description        string `json:"description"`
	Address            string `json:"address"`
	SSHPort            int    `json:"sshPort"`
	SSHUser            string `json:"sshUser"`
	HostKeyFingerprint string `json:"hostKeyFingerprint"`
}

// updateNode applies the Connection card edits. The locally managed server
// only accepts name and description changes: its "connection" is the panel
// process itself, not an SSH target.
func (s *Server) updateNode(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	n, err := s.store.Nodes.NodeByID(r.Context(), id)
	if err != nil {
		mapError(w, err)
		return
	}

	var req nodeUpdateRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "server name is required")
		return
	}

	n.Name = strings.TrimSpace(req.Name)
	n.Description = strings.TrimSpace(req.Description)
	if n.ID != db.LocalNodeID {
		if strings.TrimSpace(req.Address) == "" {
			writeError(w, http.StatusBadRequest, "server address is required")
			return
		}
		port := req.SSHPort
		if port <= 0 || port > 65535 {
			writeError(w, http.StatusBadRequest, "ssh port must be between 1 and 65535")
			return
		}
		user := strings.TrimSpace(req.SSHUser)
		if user == "" {
			user = "root"
		}
		addressChanged := n.Address != strings.TrimSpace(req.Address) ||
			n.SSHPort != port || n.SSHUser != user
		changed := addressChanged
		oldFingerprint := n.HostKeyFingerprint
		n.Address = strings.TrimSpace(req.Address)
		n.SSHPort = port
		n.SSHUser = user
		if provided := strings.TrimSpace(req.HostKeyFingerprint); provided != "" {
			fingerprint, err := validateHostKey(provided)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			if addressChanged && fingerprint == oldFingerprint {
				// The UI submitted the old pin alongside a new address. A
				// different host must prove itself again; the first connection
				// records the new key.
				n.HostKeyFingerprint = ""
			} else {
				if fingerprint != oldFingerprint {
					changed = true
				}
				n.HostKeyFingerprint = fingerprint
			}
		} else if addressChanged {
			n.HostKeyFingerprint = ""
		}
		if changed && s.nodes != nil {
			// The cached tunnel points at the old address or host key.
			s.nodes.Forget(n.ID)
		}
	}

	if err := s.store.Nodes.UpdateNode(r.Context(), n); err != nil {
		mapError(w, err)
		return
	}
	s.audit(r.Context(), r, "node.update", "node", id, nil)
	writeJSON(w, http.StatusOK, n)
}

// validateNode re-checks a server: SSH reachability for remote nodes, engine
// ping for the local one, and refreshes the discovered facts either way.
func (s *Server) validateNode(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	n, err := s.store.Nodes.NodeByID(r.Context(), id)
	if err != nil {
		mapError(w, err)
		return
	}

	if err := s.refreshNodeFacts(r.Context(), n); err != nil {
		s.store.Nodes.UpdateNodeStatus(r.Context(), n.ID, db.NodeError, err.Error(), time.Now().UTC())
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if err := s.store.Nodes.UpdateNodeStatus(r.Context(), n.ID, db.NodeOnline, "", time.Now().UTC()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	updated, err := s.store.Nodes.NodeByID(r.Context(), n.ID)
	if err != nil {
		mapError(w, err)
		return
	}
	s.audit(r.Context(), r, "node.validate", "node", id, nil)
	writeJSON(w, http.StatusOK, updated)
}

// refreshNodeFacts pings the node's engine and stores the facts it reports.
// The local server has no SSH leg; remote uptime rides over the SSH dialer.
func (s *Server) refreshNodeFacts(ctx context.Context, n *db.Node) error {
	if s.nodes == nil {
		if s.docker == nil {
			return errors.New("server management is not enabled")
		}
	}
	if n.ID != db.LocalNodeID && n.Address == "" {
		return errors.New("this server has no address to reach")
	}

	var (
		client *docker.Client
		err    error
	)
	if n.ID == db.LocalNodeID {
		client, err = s.clientForNode(ctx, db.LocalNodeID)
	} else {
		client, err = s.clientForNode(ctx, n.ID)
	}
	if err != nil {
		return err
	}
	if err := client.Ping(ctx); err != nil {
		return fmt.Errorf("docker engine unreachable: %w", err)
	}
	info, err := client.Info(ctx)
	if err != nil {
		return err
	}

	var bootTime *time.Time
	if n.ID != db.LocalNodeID && s.nodes != nil {
		if dialer, derr := s.nodes.Dialer(ctx, n); derr == nil {
			bootTime = sshBootTime(ctx, dialer)
		}
	}

	return s.store.Nodes.UpdateNodeFacts(ctx, n.ID,
		info.OperatingSystem, info.KernelVersion, info.Architecture, info.ServerVersion,
		info.NCPU, info.MemTotal, bootTime, time.Now().UTC())
}

// sshBootTime derives the boot timestamp from /proc/uptime over SSH. Failure
// just means "up since" stays unknown, so errors are swallowed.
func sshBootTime(ctx context.Context, dialer *node.Dialer) *time.Time {
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var output string
	if err := dialer.Run(probeCtx, "cat /proc/uptime", func(chunk string) { output += chunk }); err != nil {
		return nil
	}
	field, _, _ := strings.Cut(strings.TrimSpace(output), " ")
	seconds, err := strconv.ParseFloat(field, 64)
	if err != nil || seconds < 0 {
		return nil
	}
	boot := time.Now().Add(-time.Duration(seconds * float64(time.Second))).UTC()
	return &boot
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

	s.store.Nodes.UpdateNodeStatus(ctx, n.ID, db.NodeInstall, "", time.Now().UTC())

	var progress []string
	collect := func(line string) {
		progress = append(progress, line)
		if s.hub != nil {
			s.hub.BroadcastNode(n.ID, map[string]any{
				"nodeId": n.ID,
				"event":  "provision",
				"line":   line,
			})
		}
	}

	if err := s.nodes.Install(ctx, n, collect); err != nil {
		s.store.Nodes.UpdateNodeStatus(ctx, n.ID, db.NodeError, err.Error(), time.Now().UTC())
		return
	}

	// A successful install means the engine should be reachable through the
	// tunnel; refresh the facts it reports so the overview shows real numbers.
	if err := s.refreshNodeFacts(ctx, n); err != nil {
		s.store.Nodes.UpdateNodeStatus(ctx, n.ID, db.NodeError,
			"docker engine unreachable after install: "+err.Error(), time.Now().UTC())
		return
	}

	s.store.Nodes.UpdateNodeStatus(ctx, n.ID, db.NodeOnline, "", time.Now().UTC())
	if s.hub != nil {
		s.hub.BroadcastNode(n.ID, map[string]any{"nodeId": n.ID, "event": "online"})
	}
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
