package deploy

import (
	"io"
	"sync"
	"time"
)

// teeWriter mirrors deployment output to the durable log file and to connected
// browsers. Doing both through a single io.Writer keeps the pipeline free of
// any transport concern.
type teeWriter struct {
	file   io.Writer
	hub    Emitter
	id     string
	mu     sync.Mutex
	buffer []byte
}

func newTeeWriter(file io.Writer, hub Emitter, deploymentID string) *teeWriter {
	return &teeWriter{file: file, hub: hub, id: deploymentID}
}

// Write appends a chunk to the log file and flushes complete lines to browsers.
func (w *teeWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	n, err := w.file.Write(p)
	if err != nil {
		w.mu.Unlock()
		return n, err
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
	w.mu.Unlock()
	return n, nil
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
