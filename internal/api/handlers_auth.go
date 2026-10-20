package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/tamalmaity-dev/meghify/internal/auth"
	"github.com/tamalmaity-dev/meghify/internal/db"
	"github.com/tamalmaity-dev/meghify/internal/version"
)

type bootstrapRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Code     string `json:"code"`
}

// bootstrap creates the first admin account. It only succeeds on an empty instance.
func (s *Server) bootstrap(w http.ResponseWriter, r *http.Request) {
	hasUsers, err := s.auth.HasUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if hasUsers {
		writeError(w, http.StatusForbidden, "instance already bootstrapped")
		return
	}

	var req bootstrapRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	user, err := s.auth.Register(r.Context(), req.Email, req.Password, req.Name)
	if err != nil {
		mapError(w, err)
		return
	}
	if user.Role != db.RoleAdmin {
		s.store.Users.UpdateUserProfile(r.Context(), user.ID, user.Name, db.RoleAdmin)
		user.Role = db.RoleAdmin
	}

	// Hand the new admin a session so the UI can continue straight into setup.
	if _, token, err := s.auth.IssueSession(r.Context(), user, s.clientIP(r), r.UserAgent()); err != nil {
		s.logger.Error("issue bootstrap session", "error", err)
	} else {
		s.setSessionCookie(w, r, token, time.Now().Add(s.cfg.SessionTTL))
	}
	s.audit(r.Context(), r, "auth.bootstrap", "user", user.ID, nil)
	writeJSON(w, http.StatusCreated, user)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	user, session, token, err := s.auth.Login(r.Context(), req.Email, req.Password, req.Code,
		s.clientIP(r), r.UserAgent())
	if err != nil {
		if errors.Is(err, auth.ErrTOTPRequired) {
			// A stable machine-readable code lets the login screen reveal the
			// authenticator field without guessing from the message text.
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"error": "totp code required",
				"code":  "totp_required",
			})
			return
		}
		mapError(w, err)
		return
	}
	s.setSessionCookie(w, r, token, session.ExpiresAt)
	s.audit(r.Context(), r, "auth.login", "user", user.ID, nil)
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	session := auth.SessionFrom(r.Context())
	if session != nil {
		s.auth.Logout(r.Context(), session.ID)
	}
	clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	if user == nil {
		// A fresh install has no accounts yet; tell the UI so the sign-in
		// screen can present account creation instead of a dead login form.
		needsBootstrap := false
		if hasUsers, err := s.auth.HasUsers(r.Context()); err != nil {
			s.logger.Error("session: count users", "error", err)
		} else {
			needsBootstrap = !hasUsers
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"authenticated":  false,
			"version":        version.Version,
			"needsBootstrap": needsBootstrap,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": true,
		"user":          user,
		"version":       version.Version,
	})
}

// setSessionCookie stores the session token.
//
// The cookie is only marked Secure when the request actually arrived over TLS
// or the panel is published behind an https:// URL: every browser silently
// drops Secure cookies on plain-http origins, which would make an http://
// install impossible to sign in to.
func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	secure := r.TLS != nil || strings.HasPrefix(strings.ToLower(s.cfg.PublicURL), "https://")
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, auth.UserFrom(r.Context()))
}

type profileRequest struct {
	Name string `json:"name"`
}

// requireBrowserSession keeps account-security mutations on the cookie
// session. A scoped API token must not be able to change the owner's password
// or second factor and then inherit the account.
func requireBrowserSession(w http.ResponseWriter, r *http.Request) bool {
	if auth.APITokenFrom(r.Context()) != nil {
		writeError(w, http.StatusForbidden, "this endpoint requires a signed-in browser session")
		return false
	}
	return true
}

func (s *Server) updateProfile(w http.ResponseWriter, r *http.Request) {
	if !requireBrowserSession(w, r) {
		return
	}
	user := auth.UserFrom(r.Context())
	var req profileRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.Users.UpdateUserProfile(r.Context(), user.ID, strings.TrimSpace(req.Name), user.Role); err != nil {
		mapError(w, err)
		return
	}
	updated, err := s.store.Users.UserByID(r.Context(), user.ID)
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

type passwordRequest struct {
	Current string `json:"currentPassword"`
	New     string `json:"newPassword"`
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	if !requireBrowserSession(w, r) {
		return
	}
	user := auth.UserFrom(r.Context())
	var req passwordRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := auth.VerifyPassword(req.Current, user.PasswordHash); err != nil {
		// This is a form validation error, not an expired session: answering
		// 400 keeps the browser from redirecting to the login screen.
		writeError(w, http.StatusBadRequest, "current password is incorrect")
		return
	}
	if len(req.New) < s.cfg.PasswordMinLen {
		writeError(w, http.StatusBadRequest, "new password is too short")
		return
	}
	hash, err := auth.HashPassword(req.New)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.store.Users.UpdateUserPassword(r.Context(), user.ID, hash); err != nil {
		mapError(w, err)
		return
	}
	// Invalidate other sessions once the password changes. The current tab
	// stays signed in; logging it out here would contradict the UI message and
	// immediately 401 the next request.
	s.rotateOtherSessions(r)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// rotateOtherSessions invalidates every session for the caller except the
// browser session making the request. Password and 2FA changes use it so a
// stolen session cannot survive a credential or factor change.
func (s *Server) rotateOtherSessions(r *http.Request) {
	user := auth.UserFrom(r.Context())
	if user == nil {
		return
	}
	sessionID := ""
	if current := auth.SessionFrom(r.Context()); current != nil {
		sessionID = current.ID
	}
	if err := s.auth.RotateSessionsExcept(r.Context(), user.ID, sessionID); err != nil {
		s.logger.Warn("session rotation failed", "user", user.ID, "error", err)
	}
}

func (s *Server) totpEnroll(w http.ResponseWriter, r *http.Request) {
	if !requireBrowserSession(w, r) {
		return
	}
	user := auth.UserFrom(r.Context())
	if user.TOTPEnabled {
		writeError(w, http.StatusConflict, "two-factor is already enabled")
		return
	}
	uri, secret, err := s.auth.EnrollTOTP(r.Context(), user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"uri": uri, "secret": secret})
}

type totpRequest struct {
	Code string `json:"code"`
}

func (s *Server) totpConfirm(w http.ResponseWriter, r *http.Request) {
	if !requireBrowserSession(w, r) {
		return
	}
	user := auth.UserFrom(r.Context())
	var req totpRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.auth.ConfirmTOTP(r.Context(), user, req.Code); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Sessions created before 2FA was enabled must not bypass it. Keep the
	// current browser signed in, invalidate every other session.
	s.rotateOtherSessions(r)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type totpDisableRequest struct {
	Password string `json:"password"`
}

func (s *Server) totpDisable(w http.ResponseWriter, r *http.Request) {
	if !requireBrowserSession(w, r) {
		return
	}
	user := auth.UserFrom(r.Context())
	var req totpDisableRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.auth.DisableTOTP(r.Context(), user, req.Password); err != nil {
		mapError(w, err)
		return
	}
	// A session that was open while 2FA was active may have been the reason
	// the factor was removed; require every other browser to sign in again.
	s.rotateOtherSessions(r)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.Stats(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// search returns a light cross-resource index for the command palette.
func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	ctx := r.Context()

	results := map[string]any{}
	matches := func(name string) bool {
		return query == "" || strings.Contains(strings.ToLower(name), query)
	}

	projects, err := s.store.Projects.ListProjects(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	filteredProjects := make([]*db.Project, 0, len(projects))
	for _, p := range projects {
		if matches(p.Name) {
			filteredProjects = append(filteredProjects, p)
		}
	}
	results["projects"] = filteredProjects

	apps, err := s.store.Applications.ListApplications(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	filteredApps := make([]*db.Application, 0, len(apps))
	for _, a := range apps {
		if matches(a.Name) {
			filteredApps = append(filteredApps, a)
		}
	}
	results["applications"] = filteredApps

	filteredNodes := make([]*db.Node, 0)
	if user := auth.UserFrom(ctx); user != nil && user.Role == db.RoleAdmin {
		nodes, err := s.store.Nodes.ListNodes(ctx)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		for _, n := range nodes {
			if matches(n.Name) {
				filteredNodes = append(filteredNodes, n)
			}
		}
	}
	// Server addresses and SSH fingerprints are admin-only; members still get
	// projects and applications from the same search endpoint.
	results["nodes"] = filteredNodes

	writeJSON(w, http.StatusOK, results)
}

// --- users ---

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.Users.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, u := range users {
		u.PasswordHash = ""
	}
	writeJSON(w, http.StatusOK, users)
}

type createUserRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
	Role     string `json:"role"`
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	role := req.Role
	if role != db.RoleAdmin && role != db.RoleMember {
		role = db.RoleMember
	}
	// An admin inviting a member is an explicit action, so it bypasses the
	// closed-signup flag that guards self service registration.
	user, err := s.auth.RegisterAsAdmin(r.Context(), req.Email, req.Password, req.Name, role)
	if err != nil {
		mapError(w, err)
		return
	}
	s.audit(r.Context(), r, "user.create", "user", user.ID, nil)
	writeJSON(w, http.StatusCreated, user)
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	target, err := s.store.Users.UserByID(r.Context(), id)
	if err != nil {
		mapError(w, err)
		return
	}

	var req createUserRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	name := target.Name
	if strings.TrimSpace(req.Name) != "" {
		name = strings.TrimSpace(req.Name)
	}
	role := target.Role
	if req.Role != "" {
		if req.Role != db.RoleAdmin && req.Role != db.RoleMember {
			writeError(w, http.StatusBadRequest, "role must be admin or member")
			return
		}
		role = req.Role
	}

	// Never allow the last admin to demote themselves (or be demoted), which
	// would leave the instance without anyone who can administer it.
	if target.Role == db.RoleAdmin && role != db.RoleAdmin {
		admins, err := s.adminCount(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if admins <= 1 {
			writeError(w, http.StatusConflict, "the last admin cannot be demoted")
			return
		}
	}

	if err := s.store.Users.UpdateUserProfile(r.Context(), id, name, role); err != nil {
		mapError(w, err)
		return
	}
	s.audit(r.Context(), r, "user.update", "user", id, map[string]any{"role": role})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) adminCount(ctx context.Context) (int, error) {
	users, err := s.store.Users.ListUsers(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, user := range users {
		if user.Role == db.RoleAdmin {
			count++
		}
	}
	return count, nil
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	current := auth.UserFrom(r.Context())
	if current != nil && current.ID == id {
		writeError(w, http.StatusBadRequest, "you cannot delete your own account")
		return
	}
	if err := s.store.Users.DeleteUser(r.Context(), id); err != nil {
		mapError(w, err)
		return
	}
	s.audit(r.Context(), r, "user.delete", "user", id, nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- API tokens ---

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	tokens, err := s.store.Users.ListAPITokens(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tokens)
}

type createTokenRequest struct {
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	if token := auth.APITokenFrom(r.Context()); token != nil && len(token.Scopes) > 0 {
		// A scoped token must not mint an unscoped token and thereby recover
		// the owner's full privileges. Token management is browser-session
		// only for scoped clients.
		writeError(w, http.StatusForbidden, "create tokens from a signed-in browser session")
		return
	}
	var req createTokenRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	scopes := make([]string, 0, len(req.Scopes))
	seen := map[string]bool{}
	for _, raw := range req.Scopes {
		scope := strings.ToLower(strings.TrimSpace(raw))
		if scope == "" {
			continue
		}
		if !auth.ValidAPIScope(scope) {
			writeError(w, http.StatusBadRequest, "unknown token scope "+scope)
			return
		}
		if !seen[scope] {
			seen[scope] = true
			scopes = append(scopes, scope)
		}
	}
	raw, token, err := s.auth.CreateAPIToken(r.Context(), user.ID, strings.TrimSpace(req.Name), scopes, s.cfg.APITokenTTL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r.Context(), r, "token.create", "api_token", token.ID, nil)
	writeJSON(w, http.StatusCreated, map[string]any{"token": raw, "meta": token})
}

func (s *Server) deleteToken(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	id := chi.URLParam(r, "id")
	// Object-level authorization: a token can only be revoked by its owner.
	// IDs are UUIDs, but an ownership predicate keeps a future shared ID space
	// from turning this into a cross-account deletion primitive.
	if err := s.store.Users.DeleteAPITokenForUser(r.Context(), id, user.ID); err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
