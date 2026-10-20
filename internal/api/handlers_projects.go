package api

import (
	"context"
	"net/http"
	"strings"
	"time"

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
	envs, err := s.store.Projects.EnvironmentsByProject(r.Context(), id)
	if err != nil {
		mapError(w, err)
		return
	}
	for _, env := range envs {
		s.teardownEnvironment(r.Context(), env.ID)
	}
	if err := s.store.Projects.DeleteProject(r.Context(), id); err != nil {
		mapError(w, err)
		return
	}
	s.audit(r.Context(), r, "project.delete", "project", id, nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// teardownEnvironment removes the workloads inside an environment before its
// rows cascade. Best effort per resource: one unreachable node must not make it
// impossible to delete the rest of the project, but every failure is logged.
func (s *Server) teardownEnvironment(ctx context.Context, envID string) {
	apps, err := s.store.Applications.ApplicationsByEnvironment(ctx, envID)
	if err == nil {
		for _, app := range apps {
			s.teardownApplication(ctx, app, teardownOptions{
				Volumes:   true,
				Networks:  true,
				Workspace: true,
				Routes:    true,
				Images:    true,
			})
			if err := s.store.Applications.DeleteApplication(ctx, app.ID); err != nil {
				s.logger.Warn("could not delete application during environment teardown",
					"application", app.ID, "error", err)
			}
		}
	} else {
		s.logger.Warn("could not list applications during environment teardown", "environment", envID, "error", err)
	}

	services, err := s.store.Resources.ServicesByEnvironment(ctx, envID)
	if err == nil && s.pipeline != nil {
		for _, service := range services {
			// Give a hung compose down a bound, but keep enough time for a
			// normal stack with a few containers to stop cleanly.
			runCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
			if err := s.pipeline.RemoveService(runCtx, service); err != nil {
				s.logger.Warn("could not remove service stack", "service", service.ID, "error", err)
			}
			cancel()
			if err := s.store.Resources.DeleteService(ctx, service.ID); err != nil {
				s.logger.Warn("could not delete service during environment teardown", "service", service.ID, "error", err)
			} else {
				s.removeServiceFiles(service.ID)
			}
		}
	} else if err != nil {
		s.logger.Warn("could not list services during environment teardown", "environment", envID, "error", err)
	}

	databases, err := s.store.Resources.DatabasesByEnvironment(ctx, envID)
	if err == nil {
		for _, database := range databases {
			s.teardownDatabase(ctx, database, true)
			if err := s.store.Resources.DeleteDatabase(ctx, database.ID); err != nil {
				s.logger.Warn("could not delete database during environment teardown", "database", database.ID, "error", err)
			}
		}
	} else {
		s.logger.Warn("could not list databases during environment teardown", "environment", envID, "error", err)
	}
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
	if _, err := s.store.Projects.EnvironmentByID(r.Context(), id); err != nil {
		mapError(w, err)
		return
	}
	s.teardownEnvironment(r.Context(), id)
	if err := s.store.Projects.DeleteEnvironment(r.Context(), id); err != nil {
		mapError(w, err)
		return
	}
	s.audit(r.Context(), r, "environment.delete", "environment", id, nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
