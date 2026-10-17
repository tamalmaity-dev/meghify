package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/tamalmaity-dev/meghify/internal/db"
)

// deployRequest starts a deployment for an application.
type deployRequest struct {
	NoCache bool `json:"noCache"`
}

func (s *Server) deployApplication(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	app, err := s.store.Applications.ApplicationByID(r.Context(), appID)
	if err != nil {
		mapError(w, err)
		return
	}

	var req deployRequest
	if r.ContentLength > 0 {
		if err := decode(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	// Applications created before automatic domains existed get their sslip.io
	// address on their first deploy.
	s.ensureDefaultDomain(r.Context(), app)

	deployment, err := s.queueDeployment(r.Context(), app, db.TriggerManual, req.NoCache, "", "")
	switch {
	case errors.Is(err, errDeploymentRunning):
		writeError(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.audit(r.Context(), r, "deployment.create", "deployment", deployment.ID,
		map[string]any{"application": app.Name})
	writeJSON(w, http.StatusAccepted, deployment)
}

// errDeploymentRunning reports a build already in flight for an application.
// The manual endpoint surfaces it as a 409; webhooks treat it as a no-op.
var errDeploymentRunning = errors.New("a deployment is already running")

// queueDeployment records a deployment and enqueues the job that runs it. The
// manual deploy endpoint and GitHub push webhooks share it, so both paths
// enforce the same "one build at a time" rule.
func (s *Server) queueDeployment(
	ctx context.Context,
	app *db.Application,
	trigger string,
	noCache bool,
	commitSHA, commitMessage string,
) (*db.Deployment, error) {
	// Never run two builds for the same application concurrently.
	if running, err := s.store.Deployments.LatestDeployment(ctx, app.ID); err == nil && running != nil {
		if isInFlight(running.Status) {
			return nil, errDeploymentRunning
		}
	}

	deployment := &db.Deployment{
		ID:            db.NewID(),
		ApplicationID: app.ID,
		Status:        db.DeployQueued,
		Trigger:       trigger,
		CommitSHA:     commitSHA,
		CommitMessage: commitMessage,
		QueuedAt:      time.Now().UTC(),
	}
	if err := s.store.Deployments.CreateDeployment(ctx, deployment); err != nil {
		return nil, err
	}

	payload, err := json.Marshal(deployPayload{
		DeploymentID:  deployment.ID,
		ApplicationID: app.ID,
		NoCache:       noCache,
	})
	if err != nil {
		return nil, err
	}

	if _, err := s.store.Jobs.Enqueue(ctx, db.JobDeploy, string(payload), time.Now(), 1); err != nil {
		return nil, err
	}
	return deployment, nil
}

// deployPayload mirrors deploy.Payload without importing the package here.
type deployPayload struct {
	DeploymentID  string `json:"deploymentId"`
	ApplicationID string `json:"applicationId"`
	NoCache       bool   `json:"noCache"`
}

func isInFlight(status string) bool {
	switch status {
	case db.DeployQueued, db.DeployBuilding, db.DeployPushing, db.DeployDeploying:
		return true
	}
	return false
}

func (s *Server) restartApplication(w http.ResponseWriter, r *http.Request) {
	s.containerAction(w, r, "restart")
}

func (s *Server) stopApplication(w http.ResponseWriter, r *http.Request) {
	s.containerAction(w, r, "stop")
}

func (s *Server) containerAction(w http.ResponseWriter, r *http.Request, action string) {
	appID := chi.URLParam(r, "id")
	app, err := s.store.Applications.ApplicationByID(r.Context(), appID)
	if err != nil {
		mapError(w, err)
		return
	}
	if s.docker == nil {
		writeError(w, http.StatusServiceUnavailable, "no docker engine configured")
		return
	}

	container, err := s.docker.ContainerByName(r.Context(), app.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if container == nil {
		app.Status = db.StatusStopped
		s.store.Applications.UpdateApplication(r.Context(), app)
		writeJSON(w, http.StatusOK, map[string]string{"status": db.StatusStopped})
		return
	}

	timeout := 20 * time.Second
	switch action {
	case "restart":
		err = s.docker.RestartContainer(r.Context(), container.ID, timeout)
		app.Status = db.StatusRunning
	default:
		err = s.docker.StopContainer(r.Context(), container.ID, timeout)
		app.Status = db.StatusStopped
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.store.Applications.UpdateApplication(r.Context(), app)

	if s.hub != nil {
		s.hub.BroadcastApplication(appID, map[string]any{
			"applicationId": appID,
			"event":         action,
			"status":        app.Status,
		})
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": app.Status})
}

func (s *Server) applicationLogs(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	app, err := s.store.Applications.ApplicationByID(r.Context(), appID)
	if err != nil {
		mapError(w, err)
		return
	}
	if s.docker == nil {
		writeError(w, http.StatusServiceUnavailable, "no docker engine configured")
		return
	}

	container, err := s.docker.ContainerByName(r.Context(), app.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if container == nil {
		writeError(w, http.StatusNotFound, "container is not running")
		return
	}

	tail := r.URL.Query().Get("tail")
	if tail == "" {
		tail = "500"
	}
	follow := r.URL.Query().Get("follow") == "1"
	reader, err := s.docker.ContainerLogs(r.Context(), container.ID, tail, follow)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer reader.Close()

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	streamDockerLogs(w, reader)
}

func (s *Server) listDeployments(w http.ResponseWriter, r *http.Request) {
	limit := 50
	deployments, err := s.store.Deployments.DeploymentsByApplication(r.Context(), chi.URLParam(r, "id"), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, deployments)
}

func (s *Server) getDeployment(w http.ResponseWriter, r *http.Request) {
	deployment, err := s.store.Deployments.DeploymentByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, deployment)
}

// deploymentLogs replays the stored build output for a finished deployment.
func (s *Server) deploymentLogs(w http.ResponseWriter, r *http.Request) {
	deployment, err := s.store.Deployments.DeploymentByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		mapError(w, err)
		return
	}

	content, readErr := readLogFile(deployment.LogPath)
	if readErr != nil {
		writeError(w, http.StatusNotFound, "no logs available for this deployment")
		return
	}

	if r.URL.Query().Get("follow") == "1" {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: " + jsonEscape(content) + "\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		s.streamDeploymentStatus(w, r, deployment.ID)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(content))
}

func (s *Server) cancelDeployment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	deployment, err := s.store.Deployments.DeploymentByID(r.Context(), id)
	if err != nil {
		mapError(w, err)
		return
	}
	if !isInFlight(deployment.Status) {
		writeError(w, http.StatusConflict, "deployment is not running")
		return
	}

	// Cancel the running work first: this stops the build context and removes
	// the helper container, so a hung build actually dies.
	if s.pipeline != nil {
		s.pipeline.Cancel(id)
	}

	if err := s.store.Deployments.FinishDeployment(r.Context(), id, db.DeployCanceled, "canceled by user"); err != nil {
		mapError(w, err)
		return
	}

	// Restore the application status: the previous container may still be
	// serving traffic while the canceled deployment is gone.
	if app, err := s.store.Applications.ApplicationByID(r.Context(), deployment.ApplicationID); err == nil {
		status := db.StatusError
		if s.docker != nil {
			if container, err := s.docker.ContainerByName(r.Context(), app.Name); err == nil && container != nil {
				status = db.StatusStopped
				if container.State == "running" {
					status = db.StatusRunning
				}
			}
		}
		_ = s.store.Applications.PatchApplicationStatus(r.Context(), app.ID, status)
		if s.hub != nil {
			s.hub.BroadcastApplication(app.ID, map[string]any{
				"applicationId": app.ID,
				"event":         "canceled",
				"status":        status,
			})
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": db.DeployCanceled})
}

func (s *Server) clearDeployments(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.store.Deployments.DeleteDeploymentsForApplication(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- job queue visibility ---

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	// Host-metrics samples are pruned quickly, but hide the kind outright so the
	// queue page can never be flooded by telemetry again. Every row it shows is
	// work someone (or a schedule) actually asked for.
	list, err := s.store.Jobs.JobsExcludingKind(r.Context(), status, db.JobNodeMetrics, 100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) retryJob(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.store.Jobs.Requeue(r.Context(), id); err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": db.JobPending})
}
