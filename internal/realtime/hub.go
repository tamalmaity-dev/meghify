// Package realtime streams deployment progress and container logs to browsers.
//
// Server-Sent Events carry pipeline output and status changes. WebSockets are
// only opened for interactive terminals, where the client must send keystrokes.
// SSE is cheaper to hold open than a WebSocket hub, which matters when the whole
// goal is a small memory footprint.
package realtime

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// Hub fans events out to subscribed clients.
type Hub struct {
	mu      sync.RWMutex
	clients map[*client]struct{}

	// Buffer bounds how much history a late subscriber can replay.
	buffer    []Event
	bufferMax int
}

// Event is one broadcast payload.
type Event struct {
	Channel string          `json:"channel"`
	Payload json.RawMessage `json:"payload"`
	At      time.Time       `json:"at"`
}

type client struct {
	ch     chan Event
	closed bool
	mu     sync.Mutex
}

func newClient(depth int) *client {
	return &client{ch: make(chan Event, depth)}
}

func (c *client) send(e Event) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	select {
	case c.ch <- e:
		return true
	default:
		// A slow consumer is dropped rather than allowed to grow the buffer.
		// Closing the channel is essential: the Stream goroutine is selecting
		// on it and would otherwise stay alive forever after remove().
		c.closed = true
		close(c.ch)
		return false
	}
}

func (c *client) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	close(c.ch)
}

// NewHub builds a hub.
func NewHub(bufferSize int) *Hub {
	if bufferSize <= 0 {
		bufferSize = 100
	}
	return &Hub{
		clients:   map[*client]struct{}{},
		buffer:    make([]Event, 0, bufferSize),
		bufferMax: bufferSize,
	}
}

// Broadcast sends an event to every subscriber and keeps it for replay.
func (h *Hub) Broadcast(channel string, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	event := Event{Channel: channel, Payload: raw, At: time.Now().UTC()}

	h.mu.Lock()
	h.buffer = append(h.buffer, event)
	if len(h.buffer) > h.bufferMax {
		h.buffer = h.buffer[len(h.buffer)-h.bufferMax:]
	}
	targets := make([]*client, 0, len(h.clients))
	for c := range h.clients {
		targets = append(targets, c)
	}
	h.mu.Unlock()

	for _, c := range targets {
		if !c.send(event) {
			h.remove(c)
		}
	}
}

// BroadcastDeployment publishes deployment progress for the UI.
func (h *Hub) BroadcastDeployment(deploymentID string, payload any) {
	h.Broadcast("deployment:"+deploymentID, payload)
}

// BroadcastApplication publishes application lifecycle changes.
func (h *Hub) BroadcastApplication(applicationID string, payload any) {
	h.Broadcast("application:"+applicationID, payload)
}

// BroadcastNode publishes server reachability changes on the node channel.
func (h *Hub) BroadcastNode(nodeID string, payload any) {
	h.Broadcast("node:"+nodeID, payload)
}

func (h *Hub) remove(c *client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
	c.close()
}

// Stream handles one SSE connection.
//
// Channel is passed as a query parameter, e.g. /stream?channel=deployment:abc.
// The newest events are replayed immediately so a late subscriber never loses
// the last few lines of a build; pass replay=0 to skip the replay when the
// client already loaded the durable history (deployment logs).
func (h *Hub) Stream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	channel := r.URL.Query().Get("channel")
	if !validChannel(channel) {
		// A global replay would hand every authenticated user the build logs of
		// every application. Subscribers must name the stream they need.
		http.Error(w, "a deployment, application or node channel is required", http.StatusBadRequest)
		return
	}
	replay := r.URL.Query().Get("replay") != "0"
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	c := newClient(64)
	h.mu.Lock()
	h.clients[c] = struct{}{}
	history := append([]Event(nil), h.buffer...)
	h.mu.Unlock()

	defer func() {
		h.remove(c)
	}()

	if replay {
		for _, e := range history {
			if channel != "" && e.Channel != channel {
				continue
			}
			if !writeEvent(w, e) {
				return
			}
		}
	}
	flusher.Flush()

	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-keepalive.C:
			// Comment frames keep proxies from closing an idle stream.
			if _, err := w.Write([]byte(": keepalive\n\n")); err != nil {
				return
			}
			flusher.Flush()
		case e, ok := <-c.ch:
			if !ok {
				return
			}
			if channel != "" && e.Channel != channel {
				continue
			}
			if !writeEvent(w, e) {
				return
			}
			flusher.Flush()
		}
	}
}

func writeEvent(w http.ResponseWriter, e Event) bool {
	payload, err := json.Marshal(e)
	if err != nil {
		return true
	}
	if _, err := w.Write([]byte("data: ")); err != nil {
		return false
	}
	if _, err := w.Write(payload); err != nil {
		return false
	}
	_, err = w.Write([]byte("\n\n"))
	return err == nil
}

// validChannel accepts only the namespaced streams the panel actually emits.
func validChannel(channel string) bool {
	prefixes := []string{"deployment:", "application:", "node:"}
	for _, prefix := range prefixes {
		if !strings.HasPrefix(channel, prefix) {
			continue
		}
		rest := strings.TrimPrefix(channel, prefix)
		if rest == "" {
			return false
		}
		for _, r := range rest {
			if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') &&
				(r < '0' || r > '9') && r != '-' && r != '_' {
				return false
			}
		}
		return true
	}
	return false
}

// Mount registers the SSE endpoint.
func (h *Hub) Mount(r chi.Router) {
	r.Get("/stream", h.Stream)
}

// SubscriberCount reports the number of open streams.
func (h *Hub) SubscriberCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// Close drops every subscriber.
func (h *Hub) Close() {
	h.mu.Lock()
	clients := make([]*client, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.clients = map[*client]struct{}{}
	h.mu.Unlock()

	for _, c := range clients {
		c.close()
	}
}
