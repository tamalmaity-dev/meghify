package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
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
	container, err := client.ContainerByName(r.Context(), app.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if container == nil {
		writeError(w, http.StatusNotFound, "container is not running")
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // browser requests are same-origin
	})
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

	pumpTerminal(ctx, conn, session.Conn)
}

// controlFrame is the JSON envelope the browser uses to resize the TTY.
type controlFrame struct {
	Type string `json:"type"`
	Rows uint   `json:"rows"`
	Cols uint   `json:"cols"`
}

// pumpTerminal shuttles bytes between the browser and the container until either
// side closes the connection. Keystrokes pass straight through; resize frames
// are JSON control messages that update the pty dimensions.
func pumpTerminal(ctx context.Context, conn *websocket.Conn, tty interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
}) {
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
