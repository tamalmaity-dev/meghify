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
// off, so the browser sees clean lines. It keeps any partial frame between
// reads, which lets the same code serve a following stream.
func streamDockerLogs(w http.ResponseWriter, reader io.ReadCloser) {
	flusher, canFlush := w.(http.Flusher)
	demux := &dockerLogDemuxer{}
	buf := make([]byte, 32*1024)

	for {
		n, err := reader.Read(buf)
		if n > 0 {
			if werr := demux.write(w, buf[:n]); werr != nil {
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

// dockerLogDemuxer parses Docker's non-tty stream framing (1 byte stream id,
// 3 unused bytes, 4 byte big-endian length) across read boundaries.
type dockerLogDemuxer struct {
	pending []byte
}

func (d *dockerLogDemuxer) write(w io.Writer, chunk []byte) error {
	d.pending = append(d.pending, chunk...)
	for {
		if len(d.pending) < 8 {
			return nil
		}
		frameLen := int(d.pending[4])<<24 | int(d.pending[5])<<16 | int(d.pending[6])<<8 | int(d.pending[7])
		looksFramed := d.pending[1] == 0 && d.pending[2] == 0 && d.pending[3] == 0 &&
			d.pending[0] <= 2 && frameLen > 0 && frameLen <= 16<<20
		if !looksFramed {
			// A tty stream (or something unrelated): pass everything through.
			_, err := w.Write(d.pending)
			d.pending = nil
			return err
		}
		if len(d.pending) < 8+frameLen {
			return nil
		}
		if _, err := w.Write(d.pending[8 : 8+frameLen]); err != nil {
			return err
		}
		d.pending = append(d.pending[:0], d.pending[8+frameLen:]...)
	}
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
