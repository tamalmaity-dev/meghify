package deploy

import (
	"context"
	"testing"

	"github.com/tamalmaity-dev/meghify/internal/db"
)

func TestACMEEmailFallsBackToFirstAdmin(t *testing.T) {
	handle, err := db.OpenPath("file:" + t.TempDir() + "/test.db?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer handle.Close()
	store := db.OpenStore(handle)
	pipeline := &Pipeline{store: store}
	ctx := context.Background()

	if got := pipeline.ACMEEmail(ctx); got != "" {
		t.Fatalf("empty database returned ACME email %q", got)
	}

	if err := store.Users.CreateUser(ctx, &db.User{
		ID:           "user-1",
		Email:        "admin@example.com",
		PasswordHash: "hash",
		Role:         db.RoleAdmin,
	}); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if got := pipeline.ACMEEmail(ctx); got != "admin@example.com" {
		t.Fatalf("fallback ACME email = %q, want the first admin's signup email", got)
	}

	pipeline.proxyEmail = "ops@example.com"
	if got := pipeline.ACMEEmail(ctx); got != "ops@example.com" {
		t.Fatalf("explicit ACME email = %q, want the configured address", got)
	}
}
