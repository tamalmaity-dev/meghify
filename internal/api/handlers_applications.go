package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/tamalmaity-dev/meghify/internal/db"
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
	return nil
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
		ID:            db.NewID(),
		EnvironmentID: envID,
		Name:          strings.TrimSpace(req.Name),
		Description:   req.Description,
		GitRepository: req.GitRepository,
		GitBranch:     valueOr(req.GitBranch, "main"),
		BuildPack:     valueOr(req.BuildPack, db.BuildDockerfile),
		Dockerfile:    valueOr(req.Dockerfile, "Dockerfile"),
		BaseDirectory: valueOr(req.BaseDirectory, "/"),
		Port:          valueOr(req.Port, 3000),
		HealthCheck:   req.HealthCheck,
		NodeID:        req.NodeID,
		FQDN:          strings.ToLower(strings.TrimSpace(req.FQDN)),
		Status:        db.StatusIdle,
	}
	if err := s.store.Applications.CreateApplication(r.Context(), app); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if app.FQDN != "" {
		s.attachDomain(r, app.ID, app.FQDN)
	}
	s.audit(r.Context(), r, "application.create", "application", app.ID, nil)
	writeJSON(w, http.StatusCreated, app)
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

	if err := s.store.Applications.UpdateApplication(r.Context(), existing); err != nil {
		mapError(w, err)
		return
	}
	s.audit(r.Context(), r, "application.update", "application", id, nil)
	writeJSON(w, http.StatusOK, existing)
}

func (s *Server) deleteApplication(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	app, err := s.store.Applications.ApplicationByID(r.Context(), id)
	if err == nil && s.docker != nil {
		if container, cerr := s.docker.ContainerByName(r.Context(), app.Name); cerr == nil && container != nil {
			s.docker.StopContainer(r.Context(), container.ID, 20)
			s.docker.RemoveContainer(r.Context(), container.ID)
		}
	}

	if err := s.store.Applications.DeleteApplication(r.Context(), id); err != nil {
		mapError(w, err)
		return
	}
	s.audit(r.Context(), r, "application.delete", "application", id, nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- domains ---

func (s *Server) listDomains(w http.ResponseWriter, r *http.Request) {
	domains, err := s.store.Applications.DomainsByApplication(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, domains)
}

type domainRequest struct {
	Host       string `json:"host"`
	TLSEnabled *bool  `json:"tlsEnabled"`
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

	appID := chi.URLParam(r, "id")
	if _, err := s.store.Applications.ApplicationByID(r.Context(), appID); err != nil {
		mapError(w, err)
		return
	}
	if existing, err := s.store.Applications.DomainByHost(r.Context(), host); err == nil && existing != nil {
		writeError(w, http.StatusConflict, "that hostname is already in use")
		return
	}

	domain := &db.Domain{
		ID:            db.NewID(),
		ApplicationID: appID,
		Host:          host,
		TLSEnabled:    req.TLSEnabled == nil || *req.TLSEnabled,
		CertStatus:    "pending",
	}
	if err := s.store.Applications.CreateDomain(r.Context(), domain); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if app, err := s.store.Applications.ApplicationByID(r.Context(), appID); err == nil {
		app.FQDN = host
		s.store.Applications.UpdateApplication(r.Context(), app)
	}

	s.audit(r.Context(), r, "domain.create", "domain", domain.ID, map[string]any{"host": host})
	writeJSON(w, http.StatusCreated, domain)
}

func (s *Server) deleteDomain(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "domainID")
	if err := s.store.Applications.DeleteDomain(r.Context(), id); err != nil {
		mapError(w, err)
		return
	}
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
		ID:            db.NewID(),
		ApplicationID: appID,
		Host:          normalized,
		TLSEnabled:    true,
		CertStatus:    "pending",
	})
}

func normalizeHost(host string) string {
	return strings.ToLower(strings.TrimSpace(host))
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
