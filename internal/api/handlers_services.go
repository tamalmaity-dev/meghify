package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/tamalmaity-dev/meghify/internal/db"
)

func (s *Server) listServices(w http.ResponseWriter, r *http.Request) {
	envID := r.URL.Query().Get("environmentId")
	if envID == "" {
		writeError(w, http.StatusBadRequest, "environmentId is required")
		return
	}
	services, err := s.store.Resources.ServicesByEnvironment(r.Context(), envID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if services == nil {
		services = []*db.Service{}
	}
	writeJSON(w, http.StatusOK, services)
}

func (s *Server) getService(w http.ResponseWriter, r *http.Request) {
	service, err := s.store.Resources.ServiceByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, service)
}

func (s *Server) createService(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name          string `json:"name"`
		EnvironmentID string `json:"environmentId"`
		NodeID        string `json:"nodeId"`
		Compose       string `json:"compose"`
	}
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "service name is required")
		return
	}
	if req.EnvironmentID == "" {
		writeError(w, http.StatusBadRequest, "environmentId is required")
		return
	}
	if strings.TrimSpace(req.Compose) == "" {
		writeError(w, http.StatusBadRequest, "compose definition is required")
		return
	}

	service := &db.Service{
		ID:            db.NewID(),
		EnvironmentID: req.EnvironmentID,
		NodeID:        req.NodeID,
		Name:          strings.TrimSpace(req.Name),
		Kind:          "compose",
		Compose:       req.Compose,
		Status:        db.StatusIdle,
	}
	if err := s.store.Resources.CreateService(r.Context(), service); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r.Context(), r, "service.create", "service", service.ID, nil)
	writeJSON(w, http.StatusCreated, service)
}

func (s *Server) updateService(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, err := s.store.Resources.ServiceByID(r.Context(), id)
	if err != nil {
		mapError(w, err)
		return
	}

	var req struct {
		Name    string `json:"name"`
		Compose string `json:"compose"`
	}
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.Name) != "" {
		existing.Name = strings.TrimSpace(req.Name)
	}
	if strings.TrimSpace(req.Compose) != "" {
		existing.Compose = req.Compose
	}
	if err := s.store.Resources.UpdateService(r.Context(), existing); err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, existing)
}

func (s *Server) deleteService(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.store.Resources.DeleteService(r.Context(), id); err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// deployService applies a Compose stack as a background job.
func (s *Server) deployService(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	service, err := s.store.Resources.ServiceByID(r.Context(), id)
	if err != nil {
		mapError(w, err)
		return
	}

	s.store.Resources.PatchServiceStatus(r.Context(), id, db.StatusDeploying)
	payload := `{"serviceId":"` + id + `"}`
	if _, err := s.store.Jobs.Enqueue(r.Context(), db.JobBuildImage, payload, time.Now(), 2); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, service)
}
