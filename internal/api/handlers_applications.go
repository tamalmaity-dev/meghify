package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/tamalmaity-dev/meghify/internal/db"
	"github.com/tamalmaity-dev/meghify/internal/deploy"
	"github.com/tamalmaity-dev/meghify/internal/github"
)

type applicationRequest struct {
	Name          string `json:"name"`
	Description   string `json:"description"`
	GitRepository string `json:"gitRepository"`
	GitBranch     string `json:"gitBranch"`
	BuildPack     string `json:"buildPack"`
	Dockerfile    string `json:"dockerfile"`
	BaseDirectory string `json:"baseDirectory"`
	Port          int    `json:"port"`
	HealthCheck   string `json:"healthCheck"`
	NodeID        string `json:"nodeId"`
	FQDN          string `json:"fqdn"`
	// Runtime settings from the Networking and Runtime cards.
	PortMappings  string  `json:"portMappings"`
	RedirectHTTPS *bool   `json:"redirectHttps"`
	CPULimit      float64 `json:"cpuLimit"`
	MemoryLimit   int64   `json:"memoryLimit"`
	// Resource guard overrides; empty/zero inherits the panel policy.
	ResourceGuardEnabled       *bool  `json:"resourceGuardEnabled"`
	ResourceGuardCPUAction     string `json:"resourceGuardCpuAction"`
	ResourceGuardMemoryAction  string `json:"resourceGuardMemoryAction"`
	ResourceGuardCPUPercent    int    `json:"resourceGuardCpuPercent"`
	ResourceGuardMemoryPercent int    `json:"resourceGuardMemoryPercent"`
	PreDeployCommand           string `json:"preDeployCommand"`
	PostDeployCommand          string `json:"postDeployCommand"`
	// GitHub linkage, set when the application is created from the GitHub App
	// repository picker. AutoDeploy defaults to true for those applications.
	GitHubInstallationID int64  `json:"githubInstallationId"`
	RepositoryFullName   string `json:"repositoryFullName"`
	AutoDeploy           *bool  `json:"autoDeploy"`
}

func (req applicationRequest) validate() error {
	if strings.TrimSpace(req.Name) == "" {
		return errValidation("application name is required")
	}
	if len(req.Name) > 100 {
		return errValidation("application name is too long")
	}
	if req.BuildPack != "" {
		switch req.BuildPack {
		case db.BuildDockerfile, db.BuildCompose, db.BuildNixpacks, db.BuildRailpack, db.BuildImage:
		default:
			return errValidation("unsupported build pack")
		}
	}
	if req.Port < 0 || req.Port > 65535 {
		return errValidation("port must be between 0 and 65535")
	}
	if req.CPULimit < 0 {
		return errValidation("cpu limit cannot be negative")
	}
	if req.MemoryLimit < 0 {
		return errValidation("memory limit cannot be negative")
	}
	if err := validateGuardCPUAction(req.ResourceGuardCPUAction); err != nil {
		return err
	}
	if err := validateGuardMemoryAction(req.ResourceGuardMemoryAction); err != nil {
		return err
	}
	if req.ResourceGuardCPUPercent < 0 || req.ResourceGuardCPUPercent > 100 {
		return errValidation("resource guard CPU threshold must be between 0 and 100 percent")
	}
	if req.ResourceGuardMemoryPercent < 0 || req.ResourceGuardMemoryPercent > 100 {
		return errValidation("resource guard memory threshold must be between 0 and 100 percent")
	}
	if _, err := parsePortMappings(req.PortMappings); err != nil {
		return err
	}
	return nil
}

func validateGuardCPUAction(action string) error {
	switch strings.TrimSpace(action) {
	case "", db.GuardActionNotify, db.GuardActionThrottle, db.GuardActionStop:
		return nil
	default:
		return errValidation("resource guard CPU action must be notify, throttle or stop")
	}
}

func validateGuardMemoryAction(action string) error {
	switch strings.TrimSpace(action) {
	case "", db.GuardActionNotify, db.GuardActionStop:
		return nil
	default:
		return errValidation("resource guard memory action must be notify or stop")
	}
}

// parsePortMappings accepts "8080:3000" or a comma/space separated list of
// host:container pairs. It returns container → host, the shape the container
// spec expects.
func parsePortMappings(raw string) (map[int]int, error) {
	mappings := map[int]int{}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return mappings, nil
	}
	for _, entry := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' }) {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		hostRaw, containerRaw, found := strings.Cut(entry, ":")
		if !found {
			return nil, errValidation("port mappings look like hostPort:containerPort, got " + entry)
		}
		host, err := strconv.Atoi(strings.TrimSpace(hostRaw))
		if err != nil || host < 1 || host > 65535 {
			return nil, errValidation("invalid host port in mapping " + entry)
		}
		container, err := strconv.Atoi(strings.TrimSpace(containerRaw))
		if err != nil || container < 1 || container > 65535 {
			return nil, errValidation("invalid container port in mapping " + entry)
		}
		mappings[container] = host
	}
	return mappings, nil
}

// cleanHealthCheck accepts an optional URL path used by the container health
// probe. The probe is built as a shell command, so shell metacharacters are
// rejected rather than escaped twice; the default is empty (no probe).
func cleanHealthCheck(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if len(value) > 512 || !strings.HasPrefix(value, "/") {
		return "", errValidation("health check must be an absolute URL path such as /health")
	}
	for _, r := range value {
		allowed := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			strings.ContainsRune("/._~%?=&+-", r)
		if !allowed {
			return "", errValidation("health check contains unsupported characters")
		}
	}
	return value, nil
}

func errValidation(message string) error { return &validationError{message} }

type validationError struct{ message string }

func (e *validationError) Error() string { return e.message }

func (s *Server) listApplications(w http.ResponseWriter, r *http.Request) {
	apps, err := s.store.Applications.ListApplications(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, apps)
}

func (s *Server) createApplication(w http.ResponseWriter, r *http.Request) {
	var req applicationRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := req.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// The environment comes from the URL so an app cannot land in the wrong project.
	envID := chi.URLParam(r, "environmentId")
	if envID == "" {
		envID = r.URL.Query().Get("environmentId")
	}
	if envID == "" {
		writeError(w, http.StatusBadRequest, "environmentId is required")
		return
	}
	if _, err := s.store.Projects.EnvironmentByID(r.Context(), envID); err != nil {
		mapError(w, err)
		return
	}
	if err := s.validateNodeSelection(r.Context(), req.NodeID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	buildPack := valueOr(req.BuildPack, db.BuildDockerfile)
	baseDirectory, err := deploy.CleanRelativePath(valueOr(req.BaseDirectory, "/"), true)
	if err != nil {
		writeError(w, http.StatusBadRequest, "baseDirectory: "+err.Error())
		return
	}
	dockerfile := req.Dockerfile
	if buildPack == db.BuildDockerfile {
		dockerfile, err = deploy.CleanRelativePath(valueOr(req.Dockerfile, "Dockerfile"), false)
		if err != nil {
			writeError(w, http.StatusBadRequest, "dockerfile: "+err.Error())
			return
		}
	}
	healthCheck, err := cleanHealthCheck(req.HealthCheck)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	app := &db.Application{
		ID:                         db.NewID(),
		EnvironmentID:              envID,
		Name:                       strings.TrimSpace(req.Name),
		Description:                req.Description,
		GitRepository:              req.GitRepository,
		GitBranch:                  valueOr(req.GitBranch, "main"),
		BuildPack:                  buildPack,
		Dockerfile:                 dockerfile,
		BaseDirectory:              baseDirectory,
		Port:                       valueOr(req.Port, 3000),
		HealthCheck:                healthCheck,
		NodeID:                     strings.TrimSpace(req.NodeID),
		FQDN:                       strings.ToLower(strings.TrimSpace(req.FQDN)),
		Status:                     db.StatusIdle,
		PortMappings:               strings.TrimSpace(req.PortMappings),
		RedirectHTTPS:              req.RedirectHTTPS == nil || *req.RedirectHTTPS,
		CPULimit:                   req.CPULimit,
		MemoryLimit:                req.MemoryLimit,
		ResourceGuardEnabled:       req.ResourceGuardEnabled == nil || *req.ResourceGuardEnabled,
		ResourceGuardCPUAction:     strings.TrimSpace(req.ResourceGuardCPUAction),
		ResourceGuardMemoryAction:  strings.TrimSpace(req.ResourceGuardMemoryAction),
		ResourceGuardCPUPercent:    req.ResourceGuardCPUPercent,
		ResourceGuardMemoryPercent: req.ResourceGuardMemoryPercent,
		PreDeployCommand:           strings.TrimSpace(req.PreDeployCommand),
		PostDeployCommand:          strings.TrimSpace(req.PostDeployCommand),
	}
	if req.GitHubInstallationID > 0 {
		if err := s.linkGitHubRepository(r, app, req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	// No domain configured? Register an sslip.io address that points at this
	// server, so the first deploy is reachable without any DNS work.
	autoAssigned := false
	if app.FQDN == "" {
		if host := s.defaultDomain(r.Context(), app); host != "" {
			app.FQDN = host
			autoAssigned = true
		}
	}
	if err := s.store.Applications.CreateApplication(r.Context(), app); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if autoAssigned {
		s.store.Applications.CreateDomain(r.Context(), &db.Domain{
			ID:             db.NewID(),
			ApplicationID:  app.ID,
			Host:           app.FQDN,
			TLSEnabled:     false,
			CertStatus:     "pending",
			RedirectHTTPS:  true,
			SearchIndexing: true,
		})
	} else if app.FQDN != "" {
		s.attachDomain(r, app.ID, app.FQDN)
	}
	s.audit(r.Context(), r, "application.create", "application", app.ID, nil)
	writeJSON(w, http.StatusCreated, app)
}

// linkGitHubRepository attaches a repository picked from a GitHub App
// installation, which enables private clones and webhook auto-deploy.
func (s *Server) linkGitHubRepository(r *http.Request, app *db.Application, req applicationRequest) error {
	installation, err := s.store.GitHub.GitHubInstallationByInstallationID(r.Context(), req.GitHubInstallationID)
	if err != nil {
		return errValidation("the selected GitHub installation is unknown — refresh the source list")
	}
	fullName := strings.TrimSpace(req.RepositoryFullName)
	if fullName == "" {
		return errValidation("repositoryFullName is required with a GitHub installation")
	}
	app.GitHubAppID = installation.GitHubAppID
	app.GitHubInstallationID = installation.InstallationID
	app.RepositoryFullName = fullName
	app.AutoDeploy = req.AutoDeploy == nil || *req.AutoDeploy

	if strings.TrimSpace(app.GitRepository) == "" {
		// The picker normally sends the canonical clone URL; fall back to the
		// source's host so a bare full name still clones.
		base := github.DefaultBaseURL
		if source, err := s.store.GitHub.GitHubAppByID(r.Context(), installation.GitHubAppID); err == nil && source.BaseURL != "" {
			base = source.BaseURL
		}
		app.GitRepository = base + "/" + fullName + ".git"
	}
	return nil
}

func (s *Server) getApplication(w http.ResponseWriter, r *http.Request) {
	app, err := s.store.Applications.ApplicationByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, app)
}

// applicationPatchRequest uses pointers so an omitted JSON field means "keep
// the stored value". The old full-record PUT let one card (for example the
// GitHub auto-deploy switch) overwrite every unrelated column with a zero
// value. PUT and PATCH share this handler; both are now partial-safe.
type applicationPatchRequest struct {
	Name                       *string  `json:"name"`
	Description                *string  `json:"description"`
	GitRepository              *string  `json:"gitRepository"`
	GitBranch                  *string  `json:"gitBranch"`
	BuildPack                  *string  `json:"buildPack"`
	Dockerfile                 *string  `json:"dockerfile"`
	BaseDirectory              *string  `json:"baseDirectory"`
	Port                       *int     `json:"port"`
	HealthCheck                *string  `json:"healthCheck"`
	NodeID                     *string  `json:"nodeId"`
	PortMappings               *string  `json:"portMappings"`
	RedirectHTTPS              *bool    `json:"redirectHttps"`
	CPULimit                   *float64 `json:"cpuLimit"`
	MemoryLimit                *int64   `json:"memoryLimit"`
	ResourceGuardEnabled       *bool    `json:"resourceGuardEnabled"`
	ResourceGuardCPUAction     *string  `json:"resourceGuardCpuAction"`
	ResourceGuardMemoryAction  *string  `json:"resourceGuardMemoryAction"`
	ResourceGuardCPUPercent    *int     `json:"resourceGuardCpuPercent"`
	ResourceGuardMemoryPercent *int     `json:"resourceGuardMemoryPercent"`
	PreDeployCommand           *string  `json:"preDeployCommand"`
	PostDeployCommand          *string  `json:"postDeployCommand"`
	AutoDeploy                 *bool    `json:"autoDeploy"`
}

func (s *Server) updateApplication(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, err := s.store.Applications.ApplicationByID(r.Context(), id)
	if err != nil {
		mapError(w, err)
		return
	}

	var req applicationPatchRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	limitsChanged := false
	guardChanged := false

	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			writeError(w, http.StatusBadRequest, "application name is required")
			return
		}
		if len(name) > 100 {
			writeError(w, http.StatusBadRequest, "application name is too long")
			return
		}
		existing.Name = name
	}
	if req.Description != nil {
		existing.Description = *req.Description
	}
	if req.GitRepository != nil {
		existing.GitRepository = strings.TrimSpace(*req.GitRepository)
	}
	if req.GitBranch != nil && strings.TrimSpace(*req.GitBranch) != "" {
		existing.GitBranch = strings.TrimSpace(*req.GitBranch)
	}
	if req.BuildPack != nil {
		pack := strings.TrimSpace(*req.BuildPack)
		switch pack {
		case db.BuildDockerfile, db.BuildCompose, db.BuildNixpacks, db.BuildRailpack, db.BuildImage:
			existing.BuildPack = pack
		default:
			writeError(w, http.StatusBadRequest, "unsupported build pack")
			return
		}
	}
	if req.Dockerfile != nil {
		existing.Dockerfile = strings.TrimSpace(*req.Dockerfile)
	}
	if req.BaseDirectory != nil {
		base, err := deploy.CleanRelativePath(*req.BaseDirectory, true)
		if err != nil {
			writeError(w, http.StatusBadRequest, "baseDirectory: "+err.Error())
			return
		}
		existing.BaseDirectory = base
	}
	if existing.BuildPack == db.BuildDockerfile {
		dockerfile, err := deploy.CleanRelativePath(existing.Dockerfile, false)
		if err != nil {
			writeError(w, http.StatusBadRequest, "dockerfile: "+err.Error())
			return
		}
		existing.Dockerfile = dockerfile
	}
	previousPort := existing.Port
	if req.Port != nil {
		if *req.Port < 0 || *req.Port > 65535 {
			writeError(w, http.StatusBadRequest, "port must be between 0 and 65535")
			return
		}
		existing.Port = *req.Port
	}
	if req.HealthCheck != nil {
		health, err := cleanHealthCheck(*req.HealthCheck)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		existing.HealthCheck = health
	}
	if req.NodeID != nil {
		current := existing.NodeID
		if current == "" {
			current = "local"
		}
		next := strings.TrimSpace(*req.NodeID)
		if next == "" {
			next = "local"
		}
		if next != current {
			// Moving a resource between engines requires stopping it on the
			// old node and recreating its networks/volumes. Node selection is
			// therefore fixed at creation time rather than half-implemented.
			writeError(w, http.StatusBadRequest, "a server cannot be changed after creation; create a new application on the target server")
			return
		}
	}
	if req.PortMappings != nil {
		mappings := strings.TrimSpace(*req.PortMappings)
		if _, err := parsePortMappings(mappings); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		existing.PortMappings = mappings
	}
	if req.CPULimit != nil {
		if *req.CPULimit < 0 {
			writeError(w, http.StatusBadRequest, "cpu limit cannot be negative")
			return
		}
		if *req.CPULimit != existing.CPULimit {
			existing.CPULimit = *req.CPULimit
			limitsChanged = true
		}
	}
	if req.MemoryLimit != nil {
		if *req.MemoryLimit < 0 {
			writeError(w, http.StatusBadRequest, "memory limit cannot be negative")
			return
		}
		if *req.MemoryLimit != existing.MemoryLimit {
			existing.MemoryLimit = *req.MemoryLimit
			limitsChanged = true
		}
	}
	if req.ResourceGuardEnabled != nil {
		if *req.ResourceGuardEnabled != existing.ResourceGuardEnabled {
			existing.ResourceGuardEnabled = *req.ResourceGuardEnabled
			guardChanged = true
		}
	}
	if req.ResourceGuardCPUAction != nil {
		action := strings.TrimSpace(*req.ResourceGuardCPUAction)
		if err := validateGuardCPUAction(action); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if action != existing.ResourceGuardCPUAction {
			existing.ResourceGuardCPUAction = action
			guardChanged = true
		}
	}
	if req.ResourceGuardMemoryAction != nil {
		action := strings.TrimSpace(*req.ResourceGuardMemoryAction)
		if err := validateGuardMemoryAction(action); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if action != existing.ResourceGuardMemoryAction {
			existing.ResourceGuardMemoryAction = action
			guardChanged = true
		}
	}
	if req.ResourceGuardCPUPercent != nil {
		if *req.ResourceGuardCPUPercent < 0 || *req.ResourceGuardCPUPercent > 100 {
			writeError(w, http.StatusBadRequest, "resource guard CPU threshold must be between 0 and 100 percent")
			return
		}
		if *req.ResourceGuardCPUPercent != existing.ResourceGuardCPUPercent {
			existing.ResourceGuardCPUPercent = *req.ResourceGuardCPUPercent
			guardChanged = true
		}
	}
	if req.ResourceGuardMemoryPercent != nil {
		if *req.ResourceGuardMemoryPercent < 0 || *req.ResourceGuardMemoryPercent > 100 {
			writeError(w, http.StatusBadRequest, "resource guard memory threshold must be between 0 and 100 percent")
			return
		}
		if *req.ResourceGuardMemoryPercent != existing.ResourceGuardMemoryPercent {
			existing.ResourceGuardMemoryPercent = *req.ResourceGuardMemoryPercent
			guardChanged = true
		}
	}
	if req.PreDeployCommand != nil {
		existing.PreDeployCommand = strings.TrimSpace(*req.PreDeployCommand)
	}
	if req.PostDeployCommand != nil {
		existing.PostDeployCommand = strings.TrimSpace(*req.PostDeployCommand)
	}
	// The Domains card's "Redirect HTTP to HTTPS" switch is the default for
	// the application's domains, so changing it applies to the existing ones.
	if req.RedirectHTTPS != nil && *req.RedirectHTTPS != existing.RedirectHTTPS {
		existing.RedirectHTTPS = *req.RedirectHTTPS
		if domains, err := s.store.Applications.DomainsByApplication(r.Context(), existing.ID); err == nil {
			for _, domain := range domains {
				domain.RedirectHTTPS = *req.RedirectHTTPS
				s.store.Applications.UpdateDomain(r.Context(), domain)
			}
		}
	}
	// Auto-deploy is toggled from the application page; the GitHub linkage
	// itself is fixed when the application is created.
	if req.AutoDeploy != nil && existing.GitHubAppID != "" {
		existing.AutoDeploy = *req.AutoDeploy
	}

	if err := s.store.Applications.UpdateApplication(r.Context(), existing); err != nil {
		mapError(w, err)
		return
	}
	// A domain that still carried the old container port as an explicit
	// override would keep routing to a port the new container no longer uses.
	// Return those overrides to "follow the application port"; a deliberate
	// different per-domain port is left alone.
	if req.Port != nil && existing.Port != previousPort {
		if domains, err := s.store.Applications.DomainsByApplication(r.Context(), existing.ID); err == nil {
			for _, domain := range domains {
				if next := inheritPort(domain.Port, previousPort); next != domain.Port {
					domain.Port = next
					s.store.Applications.UpdateDomain(r.Context(), domain)
				}
			}
		}
	}
	if limitsChanged || (guardChanged && !existing.ResourceGuardEnabled) {
		// Turning protection off also lifts a temporary CPU throttle so the
		// saved limits take effect immediately, even without a redeploy.
		s.applyApplicationResources(r.Context(), existing)
	}
	if s.guard != nil && (limitsChanged || guardChanged) {
		s.guard.ResetApplication(existing.ID)
	}
	s.syncRoutes(r.Context(), id)
	s.audit(r.Context(), r, "application.update", "application", id, nil)
	writeJSON(w, http.StatusOK, existing)
}

// deleteApplication removes an application, and — when the Coolify-style
// cleanup options are ticked — the resources it leaves behind: volume
// directories, its private network, its deploy workspace and image/build
// caches.
func (s *Server) deleteApplication(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	app, err := s.store.Applications.ApplicationByID(r.Context(), id)
	if err != nil {
		mapError(w, err)
		return
	}
	// A live deployment can create a new container after teardown has already
	// removed the old one. Require the operator to cancel it first.
	if latest, err := s.store.Deployments.LatestDeployment(r.Context(), id); err == nil && latest != nil && isInFlight(latest.Status) {
		writeError(w, http.StatusConflict, "cancel the running deployment before deleting this application")
		return
	}

	result := s.teardownApplication(r.Context(), app, teardownOptions{
		Volumes:   r.URL.Query().Get("volumes") == "1",
		Networks:  r.URL.Query().Get("networks") == "1",
		Workspace: r.URL.Query().Get("config") == "1",
		Routes:    true,
		Images:    r.URL.Query().Get("cleanup") == "1",
		Prune:     r.URL.Query().Get("cleanup") == "1",
	})

	if err := s.store.Applications.DeleteApplication(r.Context(), id); err != nil {
		mapError(w, err)
		return
	}
	s.audit(r.Context(), r, "application.delete", "application", id, result)
	writeJSON(w, http.StatusOK, result)
}

// --- domains ---

// applyApplicationLimitsNow forces the saved CPU/memory ceilings onto the
// running container. This is also how an operator clears a temporary
// resource-guard throttle without redeploying.
func (s *Server) applyApplicationLimitsNow(w http.ResponseWriter, r *http.Request) {
	app, err := s.store.Applications.ApplicationByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		mapError(w, err)
		return
	}
	if err := s.pushApplicationResources(r.Context(), app); err != nil {
		switch {
		case errors.Is(err, errNoContainer):
			writeError(w, http.StatusConflict, "the application has no running container; deploy it first")
		case errors.Is(err, errNoEngine), errors.Is(err, errEngineUnavailable):
			writeError(w, http.StatusServiceUnavailable, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	if s.guard != nil {
		s.guard.ResetApplication(app.ID)
	}
	s.audit(r.Context(), r, "application.apply_limits", "application", app.ID, nil)
	writeJSON(w, http.StatusOK, app)
}

// applyApplicationResources pushes changed CPU/memory ceilings to an existing
// container immediately. The database remains the source of truth if the
// engine is temporarily unavailable; the next deploy recreates the container
// with the same limits anyway.
func (s *Server) applyApplicationResources(ctx context.Context, app *db.Application) {
	if err := s.pushApplicationResources(ctx, app); err != nil {
		s.logger.Debug("could not apply application limits", "application", app.ID, "error", err)
	}
}

// pushApplicationResources is the strict form used by the explicit Apply
// limits button on the Runtime card.
func (s *Server) pushApplicationResources(ctx context.Context, app *db.Application) error {
	client, err := s.clientForNode(ctx, app.NodeID)
	if err != nil {
		return fmt.Errorf("%w: %v", errEngineUnavailable, err)
	}
	container, err := client.ContainerByName(ctx, deploy.ContainerName(app))
	if err != nil {
		return fmt.Errorf("%w: %v", errEngineUnavailable, err)
	}
	if container == nil {
		return errNoContainer
	}
	return client.UpdateContainerResources(ctx, container.ID, app.CPULimit, app.MemoryLimit)
}

// syncRoutes pushes the application's routing file to the proxy; failures are
// logged, never fatal — the proxy keeps serving the last good file until a
// following edit or boot reconcile fixes it.
func (s *Server) syncRoutes(ctx context.Context, appID string) {
	if s.pipeline == nil {
		return
	}
	if err := s.pipeline.SyncApplicationRoutes(ctx, appID); err != nil {
		s.logger.Warn("route sync failed", "application", appID, "error", err)
	}
}

// applicationLabels renders the container labels the next deployment attaches,
// so the UI can show the proxy wiring the way Coolify's label viewer does.
func (s *Server) applicationLabels(w http.ResponseWriter, r *http.Request) {
	app, err := s.store.Applications.ApplicationByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		mapError(w, err)
		return
	}
	domains, err := s.store.Applications.DomainsByApplication(r.Context(), app.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	yaml := deploy.DynamicConfigYAML(app, domains, s.cfg.ACMEEmail != "")
	lines := strings.Split(strings.TrimRight(yaml, "\n"), "\n")
	writeJSON(w, http.StatusOK, lines)
}

func (s *Server) listDomains(w http.ResponseWriter, r *http.Request) {
	domains, err := s.store.Applications.DomainsByApplication(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, domains)
}

type domainRequest struct {
	Host           string `json:"host"`
	TLSEnabled     *bool  `json:"tlsEnabled"`
	Path           string `json:"path"`
	Port           int    `json:"port"`
	RedirectHTTPS  *bool  `json:"redirectHttps"`
	SearchIndexing *bool  `json:"searchIndexing"`
}

func (s *Server) createDomain(w http.ResponseWriter, r *http.Request) {
	var req domainRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	host := normalizeHost(req.Host)
	if host == "" {
		writeError(w, http.StatusBadRequest, "a valid hostname is required")
		return
	}
	if req.Port < 0 || req.Port > 65535 {
		writeError(w, http.StatusBadRequest, "port must be between 0 and 65535")
		return
	}

	appID := chi.URLParam(r, "id")
	app, err := s.store.Applications.ApplicationByID(r.Context(), appID)
	if err != nil {
		mapError(w, err)
		return
	}
	if existing, err := s.store.Applications.DomainByHost(r.Context(), host); err == nil && existing != nil {
		writeError(w, http.StatusConflict, "that hostname is already in use")
		return
	}

	domain := &db.Domain{
		ID:             db.NewID(),
		ApplicationID:  appID,
		Host:           host,
		TLSEnabled:     req.TLSEnabled == nil || *req.TLSEnabled,
		CertStatus:     "pending",
		Path:           strings.TrimSpace(req.Path),
		Port:           inheritPort(req.Port, app.Port),
		RedirectHTTPS:  req.RedirectHTTPS == nil || *req.RedirectHTTPS,
		SearchIndexing: req.SearchIndexing == nil || *req.SearchIndexing,
	}
	if err := s.store.Applications.CreateDomain(r.Context(), domain); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	app.FQDN = host
	s.store.Applications.UpdateApplication(r.Context(), app)

	s.audit(r.Context(), r, "domain.create", "domain", domain.ID, map[string]any{"host": host})
	// File-based routing: the new route goes live immediately, without a
	// redeployment.
	s.syncRoutes(r.Context(), appID)
	writeJSON(w, http.StatusCreated, domain)
}

// updateDomain edits a domain's routing options from the Domains card.
func (s *Server) updateDomain(w http.ResponseWriter, r *http.Request) {
	domain, err := s.store.Applications.DomainByID(r.Context(), chi.URLParam(r, "domainID"))
	if err != nil {
		mapError(w, err)
		return
	}

	var req domainRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Port < 0 || req.Port > 65535 {
		writeError(w, http.StatusBadRequest, "port must be between 0 and 65535")
		return
	}
	if host := normalizeHost(req.Host); host != "" {
		if host != domain.Host {
			if existing, err := s.store.Applications.DomainByHost(r.Context(), host); err == nil && existing != nil {
				writeError(w, http.StatusConflict, "that hostname is already in use")
				return
			}
		}
		domain.Host = host
	}

	app, err := s.store.Applications.ApplicationByID(r.Context(), domain.ApplicationID)
	if err != nil {
		mapError(w, err)
		return
	}
	domain.Path = strings.TrimSpace(req.Path)
	domain.Port = inheritPort(req.Port, app.Port)
	if req.TLSEnabled != nil {
		domain.TLSEnabled = *req.TLSEnabled
	}
	if req.RedirectHTTPS != nil {
		domain.RedirectHTTPS = *req.RedirectHTTPS
	}
	if req.SearchIndexing != nil {
		domain.SearchIndexing = *req.SearchIndexing
	}

	if err := s.store.Applications.UpdateDomain(r.Context(), domain); err != nil {
		mapError(w, err)
		return
	}
	s.audit(r.Context(), r, "domain.update", "domain", domain.ID, nil)
	s.syncRoutes(r.Context(), domain.ApplicationID)
	writeJSON(w, http.StatusOK, domain)
}

// checkDomain resolves one domain and compares it against the panel's public
// address, storing the outcome for the DNS-status column.
func (s *Server) checkDomain(w http.ResponseWriter, r *http.Request) {
	domain, err := s.store.Applications.DomainByID(r.Context(), chi.URLParam(r, "domainID"))
	if err != nil {
		mapError(w, err)
		return
	}
	checked := s.checkDomainDNS(r.Context(), domain)
	s.audit(r.Context(), r, "domain.dns_check", "domain", checked.ID, map[string]any{"status": checked.DNSStatus})
	writeJSON(w, http.StatusOK, checked)
}

// checkApplicationDomains re-checks every domain of an application.
func (s *Server) checkApplicationDomains(w http.ResponseWriter, r *http.Request) {
	domains, err := s.store.Applications.DomainsByApplication(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	checked := make([]*db.Domain, 0, len(domains))
	for _, domain := range domains {
		checked = append(checked, s.checkDomainDNS(r.Context(), domain))
	}
	writeJSON(w, http.StatusOK, checked)
}

// checkDomainDNS resolves the hostname and records whether it points at this
// server.
func (s *Server) checkDomainDNS(ctx context.Context, domain *db.Domain) *db.Domain {
	host := strings.SplitN(domain.Host, ":", 2)[0]
	status := "mismatch"
	want := ""
	if app, err := s.store.Applications.ApplicationByID(ctx, domain.ApplicationID); err == nil {
		// Compare against the engine that actually serves this application.
		// A remote application's hostname must resolve to its node, not to the
		// panel's public address.
		want = s.appIPv4(ctx, app)
	}
	if want != "" {
		lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if addresses, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host); err == nil {
			for _, address := range addresses {
				if address.IP.String() == want {
					status = "matches"
					break
				}
			}
		}
	}
	now := time.Now().UTC()
	domain.DNSStatus = status
	domain.DNSCheckedAt = &now
	s.store.Applications.UpdateDomain(ctx, domain)
	return domain
}

// suggestDomain proposes the automatic sslip.io address for the Add-domain
// dialog's "Generate Domain" button.
func (s *Server) suggestDomain(w http.ResponseWriter, r *http.Request) {
	app, err := s.store.Applications.ApplicationByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		mapError(w, err)
		return
	}
	host := s.defaultDomain(r.Context(), app)
	if host == "" {
		writeError(w, http.StatusServiceUnavailable, "could not determine this server's public address")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"host": host})
}

func (s *Server) deleteDomain(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "domainID")
	domain, err := s.store.Applications.DomainByID(r.Context(), id)
	if err != nil {
		mapError(w, err)
		return
	}
	if err := s.store.Applications.DeleteDomain(r.Context(), id); err != nil {
		mapError(w, err)
		return
	}
	s.syncRoutes(r.Context(), domain.ApplicationID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// attachDomain creates a domain, swallowing conflicts so a duplicate fqdn on an
// existing application does not fail the create call.
func (s *Server) attachDomain(r *http.Request, appID, host string) {
	normalized := normalizeHost(host)
	if normalized == "" {
		return
	}
	if existing, err := s.store.Applications.DomainByHost(r.Context(), normalized); err == nil && existing != nil {
		return
	}
	s.store.Applications.CreateDomain(r.Context(), &db.Domain{
		ID:             db.NewID(),
		ApplicationID:  appID,
		Host:           normalized,
		TLSEnabled:     true,
		CertStatus:     "pending",
		RedirectHTTPS:  true,
		SearchIndexing: true,
	})
}

func normalizeHost(host string) string {
	return strings.ToLower(strings.TrimSpace(host))
}

// inheritPort stores the application's container port as an inherited value
// (zero) instead of a fixed per-domain override. A domain that explicitly
// names a different port keeps that override, including when the application
// port changes later.
func inheritPort(port, appPort int) int {
	if port == appPort {
		return 0
	}
	return port
}

// --- automatic domains ---

// defaultDomain renders the sslip.io address a fresh application gets when no
// domain is configured: "<name>-<short-id>.<panel-ip>.sslip.io". sslip.io
// resolves any subdomain carrying an IP back to it, so http:// works with no
// DNS setup — the same convenience Coolify offers.
func (s *Server) defaultDomain(ctx context.Context, app *db.Application) string {
	ip := s.appIPv4(ctx, app)
	if ip == "" {
		return ""
	}
	shortID := app.ID
	if len(shortID) > 6 {
		shortID = shortID[:6]
	}
	return fmt.Sprintf("%s-%s.%s.sslip.io", hostSlug(app.Name), shortID, ip)
}

// appIPv4 is the address an application's hostnames should resolve to: the
// panel's public address for local workloads, or the remote node's address.
func (s *Server) appIPv4(ctx context.Context, app *db.Application) string {
	if app == nil {
		return ""
	}
	if app.NodeID == "" || app.NodeID == "local" || app.NodeID == db.LocalNodeID {
		return s.panelIPv4(ctx)
	}
	node, err := s.store.Nodes.NodeByID(ctx, app.NodeID)
	if err != nil {
		return ""
	}
	return resolveIPv4(ctx, node.Address)
}

// panelIPv4 finds the panel's public IPv4: from PANEL_PUBLIC_URL when it holds
// an address, otherwise by resolving its hostname once.
func (s *Server) panelIPv4(ctx context.Context) string {
	public := strings.TrimSpace(s.cfg.PublicURL)
	host := public
	if parsed, err := url.Parse(public); err == nil && parsed.Host != "" {
		host = parsed.Hostname()
	} else if h, _, err := net.SplitHostPort(public); err == nil {
		host = h
	}
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return v4.String()
		}
		return ""
	}

	lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
	if err != nil {
		return ""
	}
	for _, address := range addresses {
		if v4 := address.IP.To4(); v4 != nil {
			return v4.String()
		}
	}
	return ""
}

// resolveIPv4 turns a node address into the IPv4 used for sslip.io. It accepts
// a literal address or a hostname, never a URL with a path.
func resolveIPv4(ctx context.Context, address string) string {
	host := strings.TrimSpace(address)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return v4.String()
		}
		return ""
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
	if err != nil {
		return ""
	}
	for _, address := range addresses {
		if v4 := address.IP.To4(); v4 != nil {
			return v4.String()
		}
	}
	return ""
}

// ensureDefaultDomain backfills the automatic address on the first deploy of
// applications created before automatic domains existed. Once assigned (or
// deleted by the operator) it is not re-added: FQDN stays set on the record.
func (s *Server) ensureDefaultDomain(ctx context.Context, app *db.Application) {
	if app.FQDN != "" {
		return
	}
	domains, err := s.store.Applications.DomainsByApplication(ctx, app.ID)
	if err != nil || len(domains) > 0 {
		return
	}
	host := s.defaultDomain(ctx, app)
	if host == "" {
		return
	}
	app.FQDN = host
	if err := s.store.Applications.UpdateApplication(ctx, app); err != nil {
		return
	}
	s.store.Applications.CreateDomain(ctx, &db.Domain{
		ID:             db.NewID(),
		ApplicationID:  app.ID,
		Host:           host,
		TLSEnabled:     false,
		CertStatus:     "pending",
		RedirectHTTPS:  true,
		SearchIndexing: true,
	})
}

// hostSlug lowercases a name into something a hostname accepts.
func hostSlug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "app"
	}
	if len(out) > 40 {
		out = strings.Trim(out[:40], "-")
	}
	return out
}

// --- env vars ---

func (s *Server) listEnvVars(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	vars, err := s.store.Applications.EnvVarsByTarget(r.Context(), db.TargetApplication, appID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, v := range vars {
		// Values never leave the API, even for admins.
		v.ValueEnc = ""
	}
	writeJSON(w, http.StatusOK, vars)
}

type envVarRequest struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	IsSecret bool   `json:"isSecret"`
	IsBuild  bool   `json:"isBuild"`
}

func (s *Server) upsertEnvVar(w http.ResponseWriter, r *http.Request) {
	var req envVarRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	key := strings.TrimSpace(req.Key)
	if key == "" {
		writeError(w, http.StatusBadRequest, "key is required")
		return
	}

	sealed, err := s.auth.Seal(req.Value)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	variable := &db.EnvVar{
		ID:       db.NewID(),
		Target:   db.TargetApplication,
		TargetID: chi.URLParam(r, "id"),
		Key:      key,
		ValueEnc: sealed,
		IsSecret: req.IsSecret,
		IsBuild:  req.IsBuild,
	}
	if err := s.store.Applications.UpsertEnvVar(r.Context(), variable); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	variable.ValueEnc = ""
	writeJSON(w, http.StatusOK, variable)
}

func (s *Server) deleteEnvVar(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Applications.DeleteEnvVar(r.Context(), chi.URLParam(r, "envID")); err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- volumes ---

func (s *Server) listVolumes(w http.ResponseWriter, r *http.Request) {
	volumes, err := s.store.Applications.VolumesByTarget(r.Context(), db.TargetApplication, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, volumes)
}

type volumeRequest struct {
	Name      string `json:"name"`
	MountPath string `json:"mountPath"`
	ReadOnly  bool   `json:"readOnly"`
}

// validateMountPath accepts the absolute in-container path a named volume is
// mounted at. The panel never turns this into a host path, so the important
// checks are Docker's own path grammar and preventing a record that would
// shadow "/" or contain traversal components.
func validateMountPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "/" || !strings.HasPrefix(value, "/") {
		return "", errors.New("mountPath must be an absolute container path")
	}
	if strings.ContainsRune(value, 0) || strings.ContainsAny(value, "\r\n\\") || strings.Contains(value, "..") {
		return "", errors.New("mountPath contains an invalid component")
	}
	if len(value) > 512 {
		return "", errors.New("mountPath is too long")
	}
	return value, nil
}

func (s *Server) createVolume(w http.ResponseWriter, r *http.Request) {
	var req volumeRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	mountPath, err := validateMountPath(req.MountPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	appID := chi.URLParam(r, "id")
	if _, err := s.store.Applications.ApplicationByID(r.Context(), appID); err != nil {
		mapError(w, err)
		return
	}
	id := db.NewID()
	volume := &db.Volume{
		ID:        id,
		Target:    db.TargetApplication,
		TargetID:  appID,
		Name:      name,
		MountPath: mountPath,
		// The engine-side name is derived only from the record ID. No host path
		// is accepted from clients: that was an arbitrary bind-mount and delete
		// primitive against the Docker host.
		HostPath: deploy.VolumeName(id),
		ReadOnly: req.ReadOnly,
	}
	if err := s.store.Applications.CreateVolume(r.Context(), volume); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, volume)
}

func (s *Server) deleteVolume(w http.ResponseWriter, r *http.Request) {
	volume, err := s.store.Applications.VolumeByID(r.Context(), chi.URLParam(r, "volumeID"))
	if err != nil {
		mapError(w, err)
		return
	}
	if volume.Target != db.TargetApplication {
		writeError(w, http.StatusNotFound, "volume not found")
		return
	}
	if app, err := s.store.Applications.ApplicationByID(r.Context(), volume.TargetID); err == nil {
		if client, err := s.clientForNode(r.Context(), app.NodeID); err == nil {
			if err := client.RemoveVolume(r.Context(), deploy.VolumeName(volume.ID)); err != nil {
				s.logger.Warn("could not remove named volume", "volume", volume.ID, "error", err)
			}
		}
	}
	if err := s.store.Applications.DeleteVolume(r.Context(), volume.ID); err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func valueOr[T comparable](value, fallback T) T {
	var zero T
	if value == zero {
		return fallback
	}
	return value
}
