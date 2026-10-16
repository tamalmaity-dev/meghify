package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// readLogFile loads a stored build log, returning an error when it is absent so
// the caller can answer 404.
func readLogFile(path string) (string, error) {
	if path == "" {
		return "", os.ErrNotExist
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// streamDockerLogs strips the multiplexing headers Docker adds when tty mode is
// off, so the browser sees clean lines.
func streamDockerLogs(w http.ResponseWriter, reader io.ReadCloser) {
	flusher, canFlush := w.(http.Flusher)
	buf := make([]byte, 32*1024)

	for {
		n, err := reader.Read(buf)
		if n > 0 {
			if _, werr := w.Write(stripDockerHeaders(buf[:n])); werr != nil {
				return
			}
			if canFlush {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

func stripDockerHeaders(chunk []byte) []byte {
	// Non-tty streams prefix each frame with an 8 byte header:
	// 1 byte stream id, 3 unused bytes, 4 byte big-endian frame length.
	if len(chunk) < 8 {
		return chunk
	}
	out := make([]byte, 0, len(chunk))
	for i := 0; i+8 <= len(chunk); {
		frameLen := int(chunk[i+4])<<24 | int(chunk[i+5])<<16 | int(chunk[i+6])<<8 | int(chunk[i+7])
		end := i + 8 + frameLen
		if frameLen <= 0 || end > len(chunk) {
			// Not a multiplexed frame; pass the remainder through untouched.
			out = append(out, chunk[i:]...)
			return out
		}
		out = append(out, chunk[i+8:end]...)
		i = end
	}
	return out
}

// jsonEscape flattens a log file into a single SSE-safe string.
func jsonEscape(s string) string {
	return strings.ReplaceAll(s, "\n", "\\n")
}

// streamDeploymentStatus polls the deployment status over SSE. The log file
// itself is durable, so a client that disconnects and reconnects replays it and
// then follows the status here.
func (s *Server) streamDeploymentStatus(w http.ResponseWriter, r *http.Request, deploymentID string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}
	flusher.Flush()

	last := ""
	for {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(time.Second):
		}

		deployment, err := s.store.Deployments.DeploymentByID(r.Context(), deploymentID)
		if err != nil {
			return
		}
		if deployment.Status != last {
			last = deployment.Status
			payload, _ := json.Marshal(map[string]string{"status": deployment.Status})
			if _, err := w.Write([]byte("data: " + string(payload) + "\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
		if !isInFlight(deployment.Status) {
			return
		}
	}
}
