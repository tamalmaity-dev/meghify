package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/tamalmaity-dev/meghify/internal/auth"
	"github.com/tamalmaity-dev/meghify/internal/config"
	"github.com/tamalmaity-dev/meghify/internal/db"
)

func authzTestRouter(t *testing.T) (http.Handler, *auth.Service, *db.Store) {
	t.Helper()
	handle, err := db.OpenPath("file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "authz.db")))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	store := db.OpenStore(handle)
	service := auth.NewService(store, nil, &config.Config{SessionTTL: time.Hour})
	middleware := auth.NewMiddleware(service, &config.Config{})

	router := chi.NewRouter()
	router.Use(middleware.Authenticate)
	router.With(middleware.RequireAdmin).Get("/admin", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	router.With(middleware.RequireAuth).Get("/read", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	router.With(middleware.RequireAuth).Post("/write", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	router.With(middleware.RequireAuth).Post("/apps/x/deploy", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	return router, service, store
}

func createAuthzUser(t *testing.T, store *db.Store, role string) *db.User {
	t.Helper()
	user := &db.User{
		ID:           db.NewID(),
		Email:        role + "@example.com",
		PasswordHash: "not-used-by-token-auth",
		Name:         role,
		Role:         role,
	}
	if err := store.Users.CreateUser(context.Background(), user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user
}

func requestWithToken(t *testing.T, router http.Handler, method, path, token string) int {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder.Code
}

func TestAdminRoutesRejectMembersEvenWithAdminScopedToken(t *testing.T) {
	router, service, store := authzTestRouter(t)
	member := createAuthzUser(t, store, db.RoleMember)
	raw, _, err := service.CreateAPIToken(context.Background(), member.ID, "member", []string{"read", "write", "deploy", "admin"}, time.Hour)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	if code := requestWithToken(t, router, http.MethodGet, "/admin", raw); code != http.StatusForbidden {
		t.Fatalf("member token on admin route = %d, want 403", code)
	}
}

func TestAdminTokenRequiresAdminScope(t *testing.T) {
	router, service, store := authzTestRouter(t)
	admin := createAuthzUser(t, store, db.RoleAdmin)
	scoped, _, err := service.CreateAPIToken(context.Background(), admin.ID, "read-only", []string{"read"}, time.Hour)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	if code := requestWithToken(t, router, http.MethodGet, "/admin", scoped); code != http.StatusForbidden {
		t.Fatalf("read-scoped admin token on admin route = %d, want 403", code)
	}
	adminToken, _, err := service.CreateAPIToken(context.Background(), admin.ID, "admin", []string{"admin"}, time.Hour)
	if err != nil {
		t.Fatalf("create admin token: %v", err)
	}
	if code := requestWithToken(t, router, http.MethodGet, "/admin", adminToken); code != http.StatusNoContent {
		t.Fatalf("admin-scoped token on admin route = %d, want 204", code)
	}
}

func TestTOTPRequiredErrorCarriesMachineCode(t *testing.T) {
	recorder := httptest.NewRecorder()
	mapError(recorder, auth.ErrTOTPRequired)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
	var body struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Code != "totp_required" {
		t.Fatalf("code = %q, want totp_required", body.Code)
	}
}

func TestAPITokenScopesBoundMutationsAndDeploy(t *testing.T) {
	router, service, store := authzTestRouter(t)
	admin := createAuthzUser(t, store, db.RoleAdmin)
	readOnly, _, err := service.CreateAPIToken(context.Background(), admin.ID, "read", []string{"read"}, time.Hour)
	if err != nil {
		t.Fatalf("create read token: %v", err)
	}
	if code := requestWithToken(t, router, http.MethodGet, "/read", readOnly); code != http.StatusNoContent {
		t.Fatalf("read token on read route = %d, want 204", code)
	}
	if code := requestWithToken(t, router, http.MethodPost, "/write", readOnly); code != http.StatusForbidden {
		t.Fatalf("read token on write route = %d, want 403", code)
	}

	writer, _, err := service.CreateAPIToken(context.Background(), admin.ID, "writer", []string{"read", "write"}, time.Hour)
	if err != nil {
		t.Fatalf("create write token: %v", err)
	}
	if code := requestWithToken(t, router, http.MethodPost, "/write", writer); code != http.StatusNoContent {
		t.Fatalf("write token on write route = %d, want 204", code)
	}
	if code := requestWithToken(t, router, http.MethodPost, "/apps/x/deploy", writer); code != http.StatusForbidden {
		t.Fatalf("write token on deploy route = %d, want 403", code)
	}

	deployer, _, err := service.CreateAPIToken(context.Background(), admin.ID, "deployer", []string{"read", "deploy"}, time.Hour)
	if err != nil {
		t.Fatalf("create deploy token: %v", err)
	}
	if code := requestWithToken(t, router, http.MethodPost, "/apps/x/deploy", deployer); code != http.StatusNoContent {
		t.Fatalf("deploy token on deploy route = %d, want 204", code)
	}
}
