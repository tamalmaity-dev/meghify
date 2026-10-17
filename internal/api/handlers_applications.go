package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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
	PortMappings      string  `json:"portMappings"`
	RedirectHTTPS     *bool   `json:"redirectHttps"`
	CPULimit          float64 `json:"cpuLimit"`
	MemoryLimit       int64   `json:"memoryLimit"`
	PreDeployCommand  string  `json:"preDeployCommand"`
	PostDeployCommand string  `json:"postDeployCommand"`
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
	if _, err := parsePortMappings(req.PortMappings); err != nil {
		return err
	}
	return nil
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

	app := &db.Application{
		ID:                db.NewID(),
		EnvironmentID:     envID,
		Name:              strings.TrimSpace(req.Name),
		Description:       req.Description,
		GitRepository:     req.GitRepository,
		GitBranch:         valueOr(req.GitBranch, "main"),
		BuildPack:         valueOr(req.BuildPack, db.BuildDockerfile),
		Dockerfile:        valueOr(req.Dockerfile, "Dockerfile"),
		BaseDirectory:     valueOr(req.BaseDirectory, "/"),
		Port:              valueOr(req.Port, 3000),
		HealthCheck:       req.HealthCheck,
		NodeID:            req.NodeID,
		FQDN:              strings.ToLower(strings.TrimSpace(req.FQDN)),
		Status:            db.StatusIdle,
		PortMappings:      strings.TrimSpace(req.PortMappings),
		RedirectHTTPS:     req.RedirectHTTPS == nil || *req.RedirectHTTPS,
		CPULimit:          req.CPULimit,
		MemoryLimit:       req.MemoryLimit,
		PreDeployCommand:  strings.TrimSpace(req.PreDeployCommand),
		PostDeployCommand: strings.TrimSpace(req.PostDeployCommand),
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

func (s *Server) updateApplication(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, err := s.store.Applications.ApplicationByID(r.Context(), id)
	if err != nil {
		mapError(w, err)
		return
	}

	var req applicationRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := req.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	existing.Name = strings.TrimSpace(req.Name)
	existing.Description = req.Description
	existing.GitRepository = req.GitRepository
	existing.GitBranch = valueOr(req.GitBranch, existing.GitBranch)
	existing.BuildPack = valueOr(req.BuildPack, existing.BuildPack)
	existing.Dockerfile = valueOr(req.Dockerfile, existing.Dockerfile)
	existing.BaseDirectory = valueOr(req.BaseDirectory, existing.BaseDirectory)
	existing.Port = valueOr(req.Port, existing.Port)
	existing.HealthCheck = req.HealthCheck
	existing.NodeID = req.NodeID
	existing.PortMappings = strings.TrimSpace(req.PortMappings)
	existing.CPULimit = req.CPULimit
	existing.MemoryLimit = req.MemoryLimit
	existing.PreDeployCommand = strings.TrimSpace(req.PreDeployCommand)
	existing.PostDeployCommand = strings.TrimSpace(req.PostDeployCommand)
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

	cleanVolumes := r.URL.Query().Get("volumes") == "1"
	cleanNetworks := r.URL.Query().Get("networks") == "1"
	cleanConfig := r.URL.Query().Get("config") == "1"
	runDockerCleanup := r.URL.Query().Get("cleanup") == "1"

	result := map[string]any{
		"volumesRemoved": 0,
		"networkRemoved": false,
		"configRemoved":  false,
		"spaceReclaimed": int64(0),
	}

	// The container goes first so its network and images can be removed.
	if s.docker != nil {
		if container, cerr := s.docker.ContainerByName(r.Context(), app.Name); cerr == nil && container != nil {
			s.docker.StopContainer(r.Context(), container.ID, 20)
			s.docker.RemoveContainer(r.Context(), container.ID)
		}
	}

	// Volume records are meaningless once the application is gone; the ticked
	// option controls the stored data behind them.
	removed := 0
	if volumes, err := s.store.Applications.VolumesByTarget(r.Context(), db.TargetApplication, id); err == nil {
		for _, volume := range volumes {
			if cleanVolumes && removablePath(volume.HostPath) {
				if err := os.RemoveAll(volume.HostPath); err == nil {
					removed++
				} else {
					s.logger.Warn("could not remove volume path", "path", volume.HostPath, "error", err)
				}
			}
			s.store.Applications.DeleteVolume(r.Context(), volume.ID)
		}
	}
	result["volumesRemoved"] = removed

	if cleanNetworks && s.docker != nil {
		if err := s.docker.RemoveNetwork(r.Context(), deploy.NetworkName(app)); err == nil {
			result["networkRemoved"] = true
		}
	}

	if cleanConfig {
		workspace := filepath.Join(s.cfg.DataDir, "workspaces", app.ID)
		if err := os.RemoveAll(workspace); err == nil {
			result["configRemoved"] = true
		}
	}

	if runDockerCleanup && s.docker != nil {
		var reclaimed int64
		if bytes, err := s.docker.PruneImages(r.Context()); err == nil {
			reclaimed += bytes
		}
		if bytes, err := s.docker.BuildCachePrune(r.Context()); err == nil {
			reclaimed += bytes
		}
		// The application's own built images also qualify.
		slug := "/" + strings.ToLower(strings.TrimSpace(app.Name)) + ":"
		if images, err := s.docker.ListImages(r.Context()); err == nil {
			for _, summary := range images {
				for _, tag := range summary.RepoTags {
					if strings.Contains(tag, slug) {
						_ = s.docker.RemoveImage(r.Context(), tag)
					}
				}
			}
		}
		result["spaceReclaimed"] = reclaimed
	}

	if err := s.store.Applications.DeleteApplication(r.Context(), id); err != nil {
		mapError(w, err)
		return
	}
	s.audit(r.Context(), r, "application.delete", "application", id, result)
	s.clearRoutes(r.Context(), id)
	writeJSON(w, http.StatusOK, result)
}

// removablePath reports whether a volume path is safe to delete: something
// specific, never a system directory.
func removablePath(path string) bool {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" || path == "." || path == string(filepath.Separator) {
		return false
	}
	protected := map[string]bool{
		"/": true, "/bin": true, "/boot": true, "/dev": true, "/etc": true, "/home": true,
		"/lib": true, "/opt": true, "/proc": true, "/root": true, "/run": true, "/sbin": true,
		"/srv": true, "/sys": true, "/tmp": true, "/usr": true, "/var": true,
	}
	return !protected[path]
}

// --- domains ---

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

// clearRoutes empties an application's routing file after a delete.
func (s *Server) clearRoutes(ctx context.Context, appID string) {
	if s.pipeline == nil {
		return
	}
	if err := s.pipeline.ClearApplicationRoutes(ctx, appID); err != nil {
		s.logger.Warn("route clear failed", "application", appID, "error", err)
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
		Port:           req.Port,
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

	domain.Path = strings.TrimSpace(req.Path)
	domain.Port = req.Port
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
	want := s.panelIPv4(ctx)
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

// --- automatic domains ---

// defaultDomain renders the sslip.io address a fresh application gets when no
// domain is configured: "<name>-<short-id>.<panel-ip>.sslip.io". sslip.io
// resolves any subdomain carrying an IP back to it, so http:// works with no
// DNS setup — the same convenience Coolify offers.
func (s *Server) defaultDomain(ctx context.Context, app *db.Application) string {
	ip := s.panelIPv4(ctx)
	if ip == "" {
		return ""
	}
	shortID := app.ID
	if len(shortID) > 6 {
		shortID = shortID[:6]
	}
	return fmt.Sprintf("%s-%s.%s.sslip.io", hostSlug(app.Name), shortID, ip)
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
	HostPath  string `json:"hostPath"`
	ReadOnly  bool   `json:"readOnly"`
}

func (s *Server) createVolume(w http.ResponseWriter, r *http.Request) {
	var req volumeRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.MountPath) == "" || strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "name and mountPath are required")
		return
	}
	volume := &db.Volume{
		ID:        db.NewID(),
		Target:    db.TargetApplication,
		TargetID:  chi.URLParam(r, "id"),
		Name:      strings.TrimSpace(req.Name),
		MountPath: req.MountPath,
		HostPath:  req.HostPath,
		ReadOnly:  req.ReadOnly,
	}
	if err := s.store.Applications.CreateVolume(r.Context(), volume); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, volume)
}

func (s *Server) deleteVolume(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Applications.DeleteVolume(r.Context(), chi.URLParam(r, "volumeID")); err != nil {
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
