package deploy

import (
	"io"
	"sync"
	"time"
)

// maxDeploymentLogBytes caps one durable build log. A runaway install/build can
// otherwise fill the data volume with output that nobody will read.
const maxDeploymentLogBytes = 50 << 20

// teeWriter mirrors deployment output to the durable log file and to connected
// browsers. Doing both through a single io.Writer keeps the pipeline free of
// any transport concern.
type teeWriter struct {
	file      io.Writer
	hub       Emitter
	id        string
	limit     int64
	written   int64
	truncated bool
	mu        sync.Mutex
	buffer    []byte
}

func newTeeWriter(file io.Writer, hub Emitter, deploymentID string) *teeWriter {
	return &teeWriter{file: file, hub: hub, id: deploymentID, limit: maxDeploymentLogBytes}
}

// Write appends a chunk to the log file and flushes complete lines to browsers.
// The file is capped; live output keeps flowing to the UI.
func (w *teeWriter) Write(p []byte) (int, error) {
	written := len(p)
	w.mu.Lock()
	if !w.truncated {
		remaining := w.limit - w.written
		keep := int64(len(p))
		if keep > remaining {
			keep = remaining
		}
		if keep > 0 {
			if _, err := w.file.Write(p[:keep]); err != nil {
				w.mu.Unlock()
				return 0, err
			}
			w.written += keep
		}
		if keep < int64(len(p)) {
			w.truncated = true
			notice := "\n[!] log file truncated at 50 MiB; later output is only streamed live\n"
			if _, err := w.file.Write([]byte(notice)); err != nil {
				w.mu.Unlock()
				return 0, err
			}
			// Copy before appending: p belongs to the caller and may have
			// spare capacity that a later write reuses.
			live := append([]byte(nil), p...)
			p = append(live, []byte(notice)...)
		}
	}

	w.buffer = append(w.buffer, p...)
	for {
		idx := indexByte(w.buffer, '\n')
		if idx < 0 {
			break
		}
		line := string(w.buffer[:idx])
		w.buffer = w.buffer[idx+1:]
		if w.hub != nil {
			w.hub.BroadcastDeployment(w.id, map[string]any{
				"deploymentId": w.id,
				"event":        "log",
				"line":         line,
				"at":           time.Now().UTC(),
			})
		}
	}
	// A process can emit megabytes without a newline (progress bars, binary
	// dumps). Bound the partial-line buffer as well as the log file.
	if len(w.buffer) > 64*1024 {
		if w.hub != nil {
			w.hub.BroadcastDeployment(w.id, map[string]any{
				"deploymentId": w.id,
				"event":        "log",
				"line":         string(w.buffer),
				"at":           time.Now().UTC(),
			})
		}
		w.buffer = nil
	}
	w.mu.Unlock()
	return written, nil
}

// Flush pushes any unterminated trailing line.
func (w *teeWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buffer) == 0 || w.hub == nil {
		w.buffer = nil
		return
	}
	w.hub.BroadcastDeployment(w.id, map[string]any{
		"deploymentId": w.id,
		"event":        "log",
		"line":         string(w.buffer),
		"at":           time.Now().UTC(),
	})
	w.buffer = nil
}

func indexByte(b []byte, c byte) int {
	for i, v := range b {
		if v == c {
			return i
		}
	}
	return -1
}
