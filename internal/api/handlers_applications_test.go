package api

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/tamalmaity-dev/meghify/internal/config"
	"github.com/tamalmaity-dev/meghify/internal/db"
)

func TestAppIPv4UsesApplicationTargetNode(t *testing.T) {
	handle, err := db.OpenPath("file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "target.db")))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	store := db.OpenStore(handle)
	server := &Server{store: store, cfg: &config.Config{PublicURL: "http://203.0.113.10:8080"}}
	ctx := context.Background()

	if got := server.appIPv4(ctx, &db.Application{}); got != "203.0.113.10" {
		t.Fatalf("local app address = %q, want the panel address", got)
	}

	node := &db.Node{
		ID:      "remote-1",
		Name:    "remote",
		Address: "198.51.100.7",
		SSHPort: 22,
		SSHUser: "root",
		Status:  db.NodeOnline,
	}
	if err := store.Nodes.CreateNode(ctx, node); err != nil {
		t.Fatalf("create node: %v", err)
	}
	if got := server.appIPv4(ctx, &db.Application{NodeID: node.ID}); got != "198.51.100.7" {
		t.Fatalf("remote app address = %q, want the node address", got)
	}
}

func TestClientIPIgnoresForwardedHeadersUnlessTrusted(t *testing.T) {
	request := httptest.NewRequest("GET", "/", nil)
	request.RemoteAddr = "198.51.100.1:1234"
	request.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")

	untrusted := &Server{cfg: &config.Config{TrustProxy: false}}
	if got := untrusted.clientIP(request); got != "198.51.100.1" {
		t.Fatalf("untrusted client IP = %q, want the socket address", got)
	}

	trusted := &Server{cfg: &config.Config{TrustProxy: true}}
	if got := trusted.clientIP(request); got != "203.0.113.9" {
		t.Fatalf("trusted client IP = %q, want the first forwarded address", got)
	}

	request.Header.Set("X-Forwarded-For", "not-an-ip")
	if got := trusted.clientIP(request); got != "198.51.100.1" {
		t.Fatalf("invalid forwarded IP = %q, want the socket address", got)
	}
}
