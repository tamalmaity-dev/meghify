package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/tamalmaity-dev/meghify/internal/config"
	"github.com/tamalmaity-dev/meghify/internal/db"
)

type contextKey int

const (
	ctxUser contextKey = iota
	ctxSession
	ctxAPIToken
)

// Middleware wires auth into the chi router.
type Middleware struct {
	service *Service
	cfg     *config.Config
}

// NewMiddleware builds the auth middleware.
func NewMiddleware(service *Service, cfg *config.Config) *Middleware {
	return &Middleware{service: service, cfg: cfg}
}

// Authenticate resolves the caller and stashes it on the request context.
// Anonymous requests pass through; use RequireAuth to reject them.
func (m *Middleware) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		token, kind := TokenFromRequest(r)

		switch kind {
		case TokenKindBearer:
			user, tok, err := m.service.APIToken(ctx, token)
			if err == nil && user != nil && tok != nil {
				ctx = context.WithValue(ctx, ctxAPIToken, tok)
				ctx = context.WithValue(ctx, ctxUser, user)
			}
		case TokenKindSession:
			user, session, err := m.service.Session(ctx, token)
			if err == nil && user != nil && session != nil {
				ctx = context.WithValue(ctx, ctxSession, session)
				ctx = context.WithValue(ctx, ctxUser, user)
			}
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireAuth rejects anonymous requests with 401 and enforces API token
// scopes. Session cookies inherit the role of their user; a scoped API token
// is limited to the actions its scopes grant.
func (m *Middleware) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := UserFrom(r.Context())
		if user == nil {
			writeAuthError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if token := APITokenFrom(r.Context()); token != nil && len(token.Scopes) > 0 {
			required := requestScope(r)
			if !tokenHasScope(token, required) {
				writeAuthError(w, http.StatusForbidden, "this API token does not have the "+required+" scope")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAdmin rejects non-admin members with 403. Scoped API tokens owned by
// an admin also need the admin scope; an unscoped token inherits its owner.
func (m *Middleware) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := UserFrom(r.Context())
		if user == nil {
			writeAuthError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if user.Role != db.RoleAdmin {
			writeAuthError(w, http.StatusForbidden, "admin role required")
			return
		}
		if token := APITokenFrom(r.Context()); token != nil && len(token.Scopes) > 0 && !tokenHasScope(token, ScopeAdmin) {
			writeAuthError(w, http.StatusForbidden, "this API token does not have the admin scope")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requestScope maps an HTTP action to the scope a token must hold. Read-only
// requests need read; deploy endpoints need deploy; every other mutation needs
// write. Admin is checked separately by RequireAdmin.
func requestScope(r *http.Request) string {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return ScopeRead
	case http.MethodPost:
		if strings.HasSuffix(strings.TrimRight(r.URL.Path, "/"), "/deploy") {
			return ScopeDeploy
		}
		return ScopeWrite
	default:
		return ScopeWrite
	}
}

func tokenHasScope(token *db.APIToken, scope string) bool {
	if token == nil || len(token.Scopes) == 0 {
		return true
	}
	for _, granted := range token.Scopes {
		if granted == scope || granted == ScopeAdmin {
			return true
		}
	}
	return false
}

// Guarded is a convenience helper that applies a method-aware guard.
func (m *Middleware) Guarded(router chi.Router, requireAdmin bool) {
	if requireAdmin {
		router.Use(m.RequireAdmin)
		return
	}
	router.Use(m.RequireAuth)
}

// UserFrom returns the authenticated user, or nil.
func UserFrom(ctx context.Context) *db.User {
	if u, ok := ctx.Value(ctxUser).(*db.User); ok {
		return u
	}
	return nil
}

// SessionFrom returns the browser session backing the request, if any.
func SessionFrom(ctx context.Context) *db.Session {
	if s, ok := ctx.Value(ctxSession).(*db.Session); ok {
		return s
	}
	return nil
}

// APITokenFrom returns the API token backing the request, if any.
func APITokenFrom(ctx context.Context) *db.APIToken {
	if t, ok := ctx.Value(ctxAPIToken).(*db.APIToken); ok {
		return t
	}
	return nil
}

func writeAuthError(w http.ResponseWriter, status int, message string) {
	writeJSONStatus(w, status, map[string]string{"error": message})
}

func writeJSONStatus(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}
