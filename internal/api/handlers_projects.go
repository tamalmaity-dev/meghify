package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/tamalmaity-dev/meghify/internal/db"
)

type createProjectRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.store.Projects.ListProjects(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, projects)
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var req createProjectRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "project name is required")
		return
	}
	if len(name) > 100 {
		writeError(w, http.StatusBadRequest, "project name is too long")
		return
	}

	project := &db.Project{ID: db.NewID(), Name: name, Description: req.Description}
	if err := s.store.Projects.CreateProject(r.Context(), project); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Every project starts with a production environment.
	if err := s.store.Projects.CreateEnvironment(r.Context(), &db.Environment{
		ID: db.NewID(), ProjectID: project.ID, Name: "production",
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r.Context(), r, "project.create", "project", project.ID, nil)
	writeJSON(w, http.StatusCreated, project)
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	project, err := s.store.Projects.ProjectByID(r.Context(), id)
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, project)
}

func (s *Server) updateProject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req createProjectRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "project name is required")
		return
	}
	if err := s.store.Projects.UpdateProject(r.Context(), id, strings.TrimSpace(req.Name), req.Description); err != nil {
		mapError(w, err)
		return
	}
	s.audit(r.Context(), r, "project.update", "project", id, nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.store.Projects.DeleteProject(r.Context(), id); err != nil {
		mapError(w, err)
		return
	}
	s.audit(r.Context(), r, "project.delete", "project", id, nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) listEnvironments(w http.ResponseWriter, r *http.Request) {
	envs, err := s.store.Projects.EnvironmentsByProject(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, envs)
}

func (s *Server) createEnvironment(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	name := strings.TrimSpace(strings.ToLower(req.Name))
	if name == "" {
		writeError(w, http.StatusBadRequest, "environment name is required")
		return
	}
	env := &db.Environment{ID: db.NewID(), ProjectID: chi.URLParam(r, "id"), Name: name}
	if err := s.store.Projects.CreateEnvironment(r.Context(), env); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, env)
}

func (s *Server) deleteEnvironment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.store.Projects.DeleteEnvironment(r.Context(), id); err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
