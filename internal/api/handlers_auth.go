package api

import (
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
	if _, token, err := s.auth.IssueSession(r.Context(), user, clientIP(r), r.UserAgent()); err != nil {
		s.logger.Error("issue bootstrap session", "error", err)
	} else {
		s.setSessionCookie(w, token, time.Now().Add(s.cfg.SessionTTL))
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
		clientIP(r), r.UserAgent())
	if err != nil {
		mapError(w, err)
		return
	}
	s.setSessionCookie(w, token, session.ExpiresAt)
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
		writeJSON(w, http.StatusOK, map[string]any{
			"authenticated": false,
			"version":       version.Version,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": true,
		"user":          user,
		"version":       version.Version,
	})
}

func (s *Server) setSessionCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.IsProduction(),
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

func (s *Server) updateProfile(w http.ResponseWriter, r *http.Request) {
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
	user := auth.UserFrom(r.Context())
	var req passwordRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := auth.VerifyPassword(req.Current, user.PasswordHash); err != nil {
		writeError(w, http.StatusUnauthorized, "current password is incorrect")
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
	// Invalidate other sessions once the password changes.
	s.auth.RotateSessions(r.Context(), user.ID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) totpEnroll(w http.ResponseWriter, r *http.Request) {
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
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type totpDisableRequest struct {
	Password string `json:"password"`
}

func (s *Server) totpDisable(w http.ResponseWriter, r *http.Request) {
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

	nodes, err := s.store.Nodes.ListNodes(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	filteredNodes := make([]*db.Node, 0, len(nodes))
	for _, n := range nodes {
		if matches(n.Name) {
			filteredNodes = append(filteredNodes, n)
		}
	}
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
	var req createUserRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	role := req.Role
	if role != db.RoleAdmin && role != db.RoleMember {
		role = db.RoleMember
	}
	if err := s.store.Users.UpdateUserProfile(r.Context(), id, req.Name, role); err != nil {
		mapError(w, err)
		return
	}
	s.audit(r.Context(), r, "user.update", "user", id, nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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
	Name string `json:"name"`
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	var req createTokenRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	raw, token, err := s.auth.CreateAPIToken(r.Context(), user.ID, strings.TrimSpace(req.Name), nil, s.cfg.APITokenTTL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r.Context(), r, "token.create", "api_token", token.ID, nil)
	writeJSON(w, http.StatusCreated, map[string]any{"token": raw, "meta": token})
}

func (s *Server) deleteToken(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.store.Users.DeleteAPIToken(r.Context(), id); err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
