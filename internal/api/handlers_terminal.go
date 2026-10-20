package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	"github.com/tamalmaity-dev/meghify/internal/db"
	"github.com/tamalmaity-dev/meghify/internal/deploy"
	"github.com/tamalmaity-dev/meghify/internal/docker"
)

// terminalSession upgrades a WebSocket to a container TTY. This is the one place
// the panel uses a WebSocket: everything else rides on SSE.
func (s *Server) terminalSession(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	app, err := s.store.Applications.ApplicationByID(r.Context(), appID)
	if err != nil {
		writeError(w, http.StatusNotFound, "application not found")
		return
	}

	client, err := s.clientForNode(r.Context(), app.NodeID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	container, err := client.ContainerByName(r.Context(), deploy.ContainerName(app))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if container == nil {
		writeError(w, http.StatusNotFound, "container is not running")
		return
	}

	// Default origin verification: a browser WebSocket must originate from the
	// same host it connected to. Disabling it let any website a signed-in admin
	// visited open an authorized shell against this panel.
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()

	ctx := r.Context()
	session, err := client.ExecInteractive(ctx, docker.ExecOptions{ContainerID: container.ID})
	if err != nil {
		conn.Close(websocket.StatusInternalError, err.Error())
		return
	}
	defer session.Conn.Close()

	pumpTerminal(ctx, conn, session.Conn, func(rows, cols uint) {
		_ = client.ResizeExec(ctx, session.ID, rows, cols)
	})
}

// --- server terminal ---

// The panel image is distroless, so no shell exists inside the panel container
// itself. The local server's terminal therefore runs in a small helper
// container that carries a shell and the docker CLI, with the engine socket
// mounted; remote servers get a real shell over their SSH connection.
const (
	terminalHelperName  = "terminal"
	terminalHelperImage = "docker:27-cli"
)

// nodeTerminal upgrades a WebSocket to an interactive shell on a server.
func (s *Server) nodeTerminal(w http.ResponseWriter, r *http.Request) {
	n, err := s.store.Nodes.NodeByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		mapError(w, err)
		return
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()

	ctx := r.Context()
	if n.ID == db.LocalNodeID {
		client, err := s.clientForNode(ctx, db.LocalNodeID)
		if err != nil {
			conn.Close(websocket.StatusInternalError, err.Error())
			return
		}
		session, err := ensureTerminalHelper(ctx, client)
		if err != nil {
			conn.Close(websocket.StatusInternalError, err.Error())
			return
		}
		defer session.Conn.Close()
		pumpTerminal(ctx, conn, session.Conn, func(rows, cols uint) {
			_ = client.ResizeExec(ctx, session.ID, rows, cols)
		})
		return
	}

	if s.nodes == nil {
		conn.Close(websocket.StatusInternalError, "server management is not enabled")
		return
	}
	dialer, err := s.nodes.Dialer(ctx, n)
	if err != nil {
		conn.Close(websocket.StatusInternalError, err.Error())
		return
	}
	shell, err := dialer.OpenShell(ctx, 0, 0)
	if err != nil {
		conn.Close(websocket.StatusInternalError, err.Error())
		return
	}
	defer shell.Close()

	pumpTerminal(ctx, conn, shell, func(rows, cols uint) { _ = shell.Resize(rows, cols) })
}

// ensureTerminalHelper starts the local terminal helper container on demand
// and opens a shell inside it. The container is created once and reused;
// restarting the panel or the container itself does not invalidate it.
func ensureTerminalHelper(ctx context.Context, client *docker.Client) (*docker.ExecSession, error) {
	container, err := client.ContainerByName(ctx, terminalHelperName)
	if err != nil {
		return nil, err
	}
	if container == nil {
		if ok, _ := client.ImageExists(ctx, terminalHelperImage); !ok {
			if err := client.PullImageRetry(ctx, terminalHelperImage, io.Discard); err != nil {
				return nil, fmt.Errorf("pull terminal image: %w", err)
			}
		}
		created, err := client.CreateContainer(ctx, &docker.ContainerSpec{
			Name:  terminalHelperName,
			Image: terminalHelperImage,
			Cmd:   []string{"sh", "-c", "while true; do sleep 3600; done"},
			Labels: map[string]string{
				"meghify.role": "terminal",
			},
			Volumes: []docker.Mount{{
				Source: "/var/run/docker.sock",
				Target: "/var/run/docker.sock",
			}},
		})
		if err != nil {
			return nil, err
		}
		container = &docker.ContainerInfo{ID: created}
	}

	if container.State != "running" {
		if err := client.StartContainer(ctx, container.ID); err != nil {
			return nil, err
		}
	}
	return client.ExecInteractive(ctx, docker.ExecOptions{ContainerID: container.ID})
}

// controlFrame is the JSON envelope the browser uses to resize the TTY.
type controlFrame struct {
	Type string `json:"type"`
	Rows uint   `json:"rows"`
	Cols uint   `json:"cols"`
}

// pumpTerminal shuttles bytes between the browser and the container or SSH
// session until either side closes the connection. Keystrokes pass straight
// through; resize frames are JSON control messages that adjust the pty.
func pumpTerminal(ctx context.Context, conn *websocket.Conn, tty interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
}, onResize func(rows, cols uint)) {
	errc := make(chan error, 2)

	go func() {
		for {
			messageType, payload, err := conn.Read(ctx)
			if err != nil {
				errc <- err
				return
			}

			// Resize frames are control messages, not terminal input.
			var frame controlFrame
			if messageType == websocket.MessageText &&
				json.Unmarshal(payload, &frame) == nil && frame.Type == "resize" {
				if onResize != nil {
					onResize(frame.Rows, frame.Cols)
				}
				continue
			}
			if _, err := tty.Write(payload); err != nil {
				errc <- err
				return
			}
		}
	}()

	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := tty.Read(buf)
			if n > 0 {
				if werr := conn.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					errc <- werr
					return
				}
			}
			if err != nil {
				errc <- err
				return
			}
		}
	}()

	select {
	case <-ctx.Done():
	case <-errc:
	}
}
