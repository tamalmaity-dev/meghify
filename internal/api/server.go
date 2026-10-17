// Package api wires HTTP routes to the panel's domain services.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/tamalmaity-dev/meghify/internal/auth"
	"github.com/tamalmaity-dev/meghify/internal/config"
	"github.com/tamalmaity-dev/meghify/internal/db"
	"github.com/tamalmaity-dev/meghify/internal/deploy"
	"github.com/tamalmaity-dev/meghify/internal/docker"
	"github.com/tamalmaity-dev/meghify/internal/jobs"
	"github.com/tamalmaity-dev/meghify/internal/node"
	"github.com/tamalmaity-dev/meghify/internal/realtime"
	"github.com/tamalmaity-dev/meghify/internal/version"
)

// Server holds every dependency the HTTP layer needs.
type Server struct {
	cfg      *config.Config
	store    *db.Store
	auth     *auth.Service
	authMW   *auth.Middleware
	hub      *realtime.Hub
	jobs     *jobs.Runner
	pipeline *deploy.Pipeline
	docker   *docker.Client
	nodes    *node.Registry
	spa      *SPAHandler
	logger   *slog.Logger
	started  time.Time
}

// Options builds a Server.
type Options struct {
	Config   *config.Config
	Store    *db.Store
	Auth     *auth.Service
	AuthMW   *auth.Middleware
	Hub      *realtime.Hub
	Jobs     *jobs.Runner
	Pipeline *deploy.Pipeline
	Docker   *docker.Client
	Nodes    *node.Registry
	SPA      *SPAHandler
	Logger   *slog.Logger
}

// NewServer builds the HTTP server.
func NewServer(opts Options) *Server {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Server{
		cfg:      opts.Config,
		store:    opts.Store,
		auth:     opts.Auth,
		authMW:   opts.AuthMW,
		hub:      opts.Hub,
		jobs:     opts.Jobs,
		pipeline: opts.Pipeline,
		docker:   opts.Docker,
		nodes:    opts.Nodes,
		spa:      opts.SPA,
		logger:   opts.Logger,
		started:  time.Now(),
	}
}

// Router builds the full route tree.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(s.logRequests)
	r.Use(s.securityHeaders)
	r.Use(middleware.Compress(5, "application/json", "text/event-stream"))

	r.Get("/api/health", s.health)
	r.Get("/api/version", s.versionInfo)

	// GitHub delivers app webhooks straight to the panel. Those requests are
	// authenticated by the app's webhook signature, not a session cookie, so
	// the endpoint must sit outside the authenticated API group.
	r.Post("/api/webhooks/github", s.gitHubWebhook)

	r.Route("/api/auth", func(r chi.Router) {
		// Resolve the caller when a session cookie is present so /session can
		// report who is signed in and /logout can revoke the right session.
		// Anonymous requests still pass through — bootstrap and login must stay
		// reachable without a session.
		r.Use(s.authMW.Authenticate)

		r.Post("/bootstrap", s.bootstrap)
		r.Post("/login", s.login)
		r.Post("/logout", s.logout)
		r.Get("/session", s.session)
	})

	r.Route("/api", func(r chi.Router) {
		r.Use(s.authMW.Authenticate)
		r.Use(s.authMW.RequireAuth)

		r.Get("/me", s.me)
		r.Put("/me", s.updateProfile)
		r.Put("/me/password", s.changePassword)
		r.Get("/me/totp", s.totpEnroll)
		r.Post("/me/totp", s.totpConfirm)
		r.Delete("/me/totp", s.totpDisable)
		r.Get("/stats", s.stats)
		r.Get("/search", s.search)

		// SSE lives behind the session gate: build output can quote source
		// paths and environment names, so anonymous listeners have no business
		// replaying it.
		s.hub.Mount(r)

		r.Route("/users", func(r chi.Router) {
			r.Get("/", s.listUsers)
			r.Post("/", s.createUser)
			r.Put("/{id}", s.updateUser)
			r.Delete("/{id}", s.deleteUser)
		})

		r.Route("/tokens", func(r chi.Router) {
			r.Get("/", s.listTokens)
			r.Post("/", s.createToken)
			r.Delete("/{id}", s.deleteToken)
		})

		r.Route("/projects", func(r chi.Router) {
			r.Get("/", s.listProjects)
			r.Post("/", s.createProject)
			r.Get("/{id}", s.getProject)
			r.Put("/{id}", s.updateProject)
			r.Delete("/{id}", s.deleteProject)

			r.Get("/{id}/environments", s.listEnvironments)
			r.Post("/{id}/environments", s.createEnvironment)
			r.Delete("/environments/{id}", s.deleteEnvironment)
		})

		r.Route("/nodes", func(r chi.Router) {
			r.Get("/", s.listNodes)
			r.Post("/", s.createNode)
			r.Get("/{id}", s.getNode)
			r.Patch("/{id}", s.updateNode)
			r.Delete("/{id}", s.deleteNode)
			r.Post("/{id}/provision", s.provisionNode)
			r.Post("/{id}/validate", s.validateNode)
			r.Get("/{id}/metrics", s.nodeMetrics)
			r.Get("/{id}/containers", s.nodeContainers)
			r.Get("/{id}/terminal", s.nodeTerminal)
		})

		r.Route("/ssh-keys", func(r chi.Router) {
			r.Get("/", s.listSSHKeys)
			r.Post("/", s.createSSHKey)
			r.Delete("/{id}", s.deleteSSHKey)
		})

		r.Route("/applications", func(r chi.Router) {
			r.Get("/", s.listApplications)
			r.Post("/", s.createApplication)
			r.Get("/{id}", s.getApplication)
			r.Put("/{id}", s.updateApplication)
			r.Delete("/{id}", s.deleteApplication)
			r.Post("/{id}/deploy", s.deployApplication)
			r.Post("/{id}/restart", s.restartApplication)
			r.Post("/{id}/stop", s.stopApplication)
			r.Get("/{id}/logs", s.applicationLogs)
			r.Get("/{id}/terminal", s.terminalSession)
			r.Get("/{id}/deployments", s.listDeployments)
			r.Delete("/{id}/deployments", s.clearDeployments)
			r.Get("/{id}/labels", s.applicationLabels)

			r.Get("/{id}/domains", s.listDomains)
			r.Post("/{id}/domains", s.createDomain)
			r.Delete("/domains/{domainID}", s.deleteDomain)

			r.Get("/{id}/env", s.listEnvVars)
			r.Put("/{id}/env", s.upsertEnvVar)
			r.Delete("/env/{envID}", s.deleteEnvVar)

			r.Get("/{id}/volumes", s.listVolumes)
			r.Post("/{id}/volumes", s.createVolume)
			r.Delete("/volumes/{volumeID}", s.deleteVolume)
		})

		r.Route("/databases", func(r chi.Router) {
			r.Get("/", s.listDatabases)
			r.Post("/", s.createDatabase)
			r.Get("/{id}", s.getDatabase)
			r.Delete("/{id}", s.deleteDatabase)
			r.Post("/{id}/start", s.startDatabase)
			r.Post("/{id}/stop", s.stopDatabase)
			r.Get("/{id}/backups", s.listBackups)
			r.Post("/{id}/backups", s.createBackup)
			r.Delete("/backups/{backupID}", s.deleteBackup)
		})

		r.Route("/services", func(r chi.Router) {
			r.Get("/", s.listServices)
			r.Post("/", s.createService)
			r.Get("/{id}", s.getService)
			r.Put("/{id}", s.updateService)
			r.Delete("/{id}", s.deleteService)
			r.Post("/{id}/deploy", s.deployService)
		})

		r.Route("/deployments", func(r chi.Router) {
			r.Get("/{id}", s.getDeployment)
			r.Get("/{id}/logs", s.deploymentLogs)
			r.Delete("/{id}", s.cancelDeployment)
		})

		r.Route("/jobs", func(r chi.Router) {
			r.Get("/", s.listJobs)
			r.Delete("/{id}", s.retryJob)
		})

		// GitHub App sources: registration (manifest flow), installations and
		// the repository lists the deployment picker reads.
		r.Route("/github", func(r chi.Router) {
			r.Get("/apps", s.listGitHubApps)
			r.Post("/apps", s.createGitHubApp)
			r.Get("/apps/{id}", s.getGitHubApp)
			r.Get("/apps/{id}/manifest", s.gitHubAppManifest)
			r.Post("/apps/{id}/credentials", s.setGitHubAppCredentials)
			r.Post("/apps/{id}/sync", s.syncGitHubAppInstallations)
			r.Delete("/apps/{id}", s.deleteGitHubApp)

			r.Get("/installations", s.listGitHubInstallations)
			r.Post("/installations/sync", s.syncGitHubInstallations)
			r.Get("/installations/{installationId}/repositories", s.listGitHubRepositories)

			// GitHub redirects the user's browser to these during the manifest
			// and installation flows; the session cookie authorizes them.
			r.Get("/callback", s.gitHubCallback)
			r.Get("/setup", s.gitHubSetup)
		})
	})

	// Anything unmatched falls through to the embedded SPA.
	r.NotFound(s.notFound)
	r.MethodNotAllowed(s.notFound)
	return r
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeError(w, http.StatusNotFound, "endpoint not found")
		return
	}
	s.serveSPA(w, r)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"uptime": time.Since(s.started).Round(time.Second).String(),
	})
}

func (s *Server) versionInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"version": version.Version,
		"commit":  version.Commit,
		"date":    version.Date,
	})
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		s.logger.Debug("request",
			"method", r.Method, "path", r.URL.Path,
			"status", ww.Status(), "duration", time.Since(start))
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		if s.cfg.IsProduction() {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// --- helpers shared by every handler ---

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// decode reads a JSON body into dst, rejecting unknown fields and bodies larger
// than 1 MB.
func decode(r *http.Request, dst any) error {
	if r.Body == nil {
		return errors.New("request body is required")
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

// mapError translates repository errors into HTTP status codes.
func mapError(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		return
	case errors.Is(err, db.ErrNotFound):
		writeError(w, http.StatusNotFound, "resource not found")
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeError(w, http.StatusUnauthorized, "invalid credentials")
	case errors.Is(err, auth.ErrTOTPRequired):
		writeError(w, http.StatusUnauthorized, "totp code required")
	case errors.Is(err, auth.ErrUserExists):
		writeError(w, http.StatusConflict, "user already exists")
	case errors.Is(err, auth.ErrSignupDisabled):
		writeError(w, http.StatusForbidden, "registration is disabled")
	case errors.Is(err, auth.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// ctxKey carries a request scoped actor.
type ctxKey struct{}

// audit records a privileged action. Audit failures never break the request.
func (s *Server) audit(ctx context.Context, r *http.Request, action, entityType, entityID string, meta map[string]any) {
	user := auth.UserFrom(ctx)
	blob, _ := json.Marshal(meta)
	entry := &db.AuditLog{
		ID:         db.NewID(),
		Action:     action,
		EntityType: entityType,
		EntityID:   entityID,
		Metadata:   string(blob),
		IP:         clientIP(r),
	}
	if user != nil {
		entry.UserID = user.ID
	}
	if err := s.store.Audit(ctx, entry); err != nil {
		s.logger.Warn("audit write failed", "action", action, "error", err)
	}
}

func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		if idx := indexOf(fwd, ','); idx > 0 {
			return fwd[:idx]
		}
		return fwd
	}
	if real := r.Header.Get("X-Real-IP"); real != "" {
		return real
	}
	return r.RemoteAddr
}

func indexOf(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
