package api

import (
	"crypto/rand"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/tamalmaity-dev/meghify/internal/db"
	"github.com/tamalmaity-dev/meghify/internal/docker"
)

// randomSecret returns a URL-safe random credential.
func randomSecret(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// databaseDefaults describes the container image used for each database kind.
type databaseDefaults struct {
	Image    string
	Version  string
	Port     int
	User     string
	DataPath string
}

func databaseSpec(kind, version string) databaseDefaults {
	specs := map[string]databaseDefaults{
		db.DBPostgres: {Image: "postgres", Version: "17-alpine", Port: 5432, User: "postgres", DataPath: "/var/lib/postgresql/data"},
		db.DBMySQL:    {Image: "mysql", Version: "8.4", Port: 3306, User: "root", DataPath: "/var/lib/mysql"},
		db.DBMariaDB:  {Image: "mariadb", Version: "11.8", Port: 3306, User: "root", DataPath: "/var/lib/mysql"},
		db.DBRedis:    {Image: "redis", Version: "7-alpine", Port: 6379, User: "", DataPath: "/data"},
		db.DBMongo:    {Image: "mongo", Version: "8", Port: 27017, User: "root", DataPath: "/data/db"},
	}
	spec := specs[kind]
	if version != "" {
		spec.Version = version
	}
	return spec
}

// dockerSpec is the internal builder for a database container.
type dockerSpec struct {
	Name    string
	Image   string
	Env     []string
	Labels  map[string]string
	Ports   map[int]int
	Restart string
	Volumes []mountSpec
}

type mountSpec struct {
	Source string
	Target string
}

// databaseEnv sets the credential each image expects. Only the matching variable
// is meaningful for a given image, so all are set and the rest are ignored.
func databaseEnv(rootPassword string) []string {
	return []string{
		"POSTGRES_PASSWORD=" + rootPassword,
		"MYSQL_ROOT_PASSWORD=" + rootPassword,
		"MARIADB_ROOT_PASSWORD=" + rootPassword,
		"MONGO_INITDB_ROOT_PASSWORD=" + rootPassword,
	}
}

func (d dockerSpec) toDocker() *docker.ContainerSpec {
	mounts := make([]docker.Mount, 0, len(d.Volumes))
	for _, v := range d.Volumes {
		mounts = append(mounts, docker.Mount{Source: v.Source, Target: v.Target})
	}
	return &docker.ContainerSpec{
		Name:          d.Name,
		Image:         d.Image,
		Env:           d.Env,
		Labels:        d.Labels,
		Ports:         d.Ports,
		Volumes:       mounts,
		RestartPolicy: "unless-stopped",
	}
}

func (s *Server) listDatabases(w http.ResponseWriter, r *http.Request) {
	databases, err := s.store.Resources.ListDatabases(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, databases)
}

func (s *Server) getDatabase(w http.ResponseWriter, r *http.Request) {
	database, err := s.store.Resources.DatabaseByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, database)
}

// createDatabase provisions a database container with generated credentials.
//
// The generated password is written into the environment_vars table encrypted at
// rest, and returned exactly once so the user can store it in their app config.
func (s *Server) createDatabase(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name          string `json:"name"`
		Kind          string `json:"kind"`
		Version       string `json:"version"`
		EnvironmentID string `json:"environmentId"`
		NodeID        string `json:"nodeId"`
	}
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	kind := strings.TrimSpace(strings.ToLower(req.Kind))
	switch kind {
	case db.DBPostgres, db.DBMySQL, db.DBMariaDB, db.DBRedis, db.DBMongo:
	default:
		writeError(w, http.StatusBadRequest, "unsupported database kind")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "database name is required")
		return
	}
	if req.EnvironmentID == "" {
		writeError(w, http.StatusBadRequest, "environmentId is required")
		return
	}

	defaults := databaseSpec(kind, req.Version)
	rootPassword, err := randomSecret(24)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	database := &db.Database{
		ID:            db.NewID(),
		EnvironmentID: req.EnvironmentID,
		NodeID:        req.NodeID,
		Name:          strings.TrimSpace(req.Name),
		Kind:          kind,
		Version:       defaults.Version,
		Status:        db.StatusDeploying,
	}
	if err := s.store.Resources.CreateDatabase(r.Context(), database); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	client, err := s.clientForNode(r.Context(), database.NodeID)
	if err != nil {
		s.store.Resources.PatchDatabaseStatus(r.Context(), database.ID, db.StatusError)
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	// Fresh VPS images are not local yet; pull the database image before creating
	// its container so the first create works without a manual docker pull.
	imageRef := defaults.Image + ":" + defaults.Version
	if err := client.PullImage(r.Context(), imageRef, io.Discard); err != nil {
		s.store.Resources.PatchDatabaseStatus(r.Context(), database.ID, db.StatusError)
		writeError(w, http.StatusInternalServerError, "pull image: "+err.Error())
		return
	}

	spec := dockerSpec{
		Name:    database.Name,
		Image:   imageRef,
		Env:     databaseEnv(rootPassword),
		Labels:  map[string]string{"meghify.resource": "database", "meghify.database": database.ID},
		Ports:   map[int]int{defaults.Port: database.PublicPort},
		Restart: "unless-stopped",
		Volumes: []mountSpec{{Source: "meghify-db-" + database.ID, Target: defaults.DataPath}},
	}
	containerID, err := client.CreateContainer(r.Context(), spec.toDocker())
	if err != nil {
		s.store.Resources.PatchDatabaseStatus(r.Context(), database.ID, db.StatusError)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := client.StartContainer(r.Context(), containerID); err != nil {
		s.store.Resources.PatchDatabaseStatus(r.Context(), database.ID, db.StatusError)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.store.Resources.PatchDatabaseStatus(r.Context(), database.ID, db.StatusRunning)

	// Store the credential so the UI can surface a connection string later.
	sealed, serr := s.auth.Seal(rootPassword)
	if serr == nil {
		s.store.Applications.UpsertEnvVar(r.Context(), &db.EnvVar{
			ID:       db.NewID(),
			Target:   db.TargetDatabase,
			TargetID: database.ID,
			Key:      "ROOT_PASSWORD",
			ValueEnc: sealed,
			IsSecret: true,
		})
	}

	s.audit(r.Context(), r, "database.create", "database", database.ID,
		map[string]any{"kind": kind})
	writeJSON(w, http.StatusCreated, map[string]any{
		"database": database,
		"password": rootPassword,
		"port":     defaults.Port,
		"user":     defaults.User,
	})
}

func (s *Server) deleteDatabase(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	database, derr := s.store.Resources.DatabaseByID(r.Context(), id)
	if derr == nil && s.docker != nil {
		if container, cerr := s.docker.ContainerByName(r.Context(), database.Name); cerr == nil && container != nil {
			s.docker.StopContainer(r.Context(), container.ID, 20)
			s.docker.RemoveContainer(r.Context(), container.ID)
		}
	}
	if err := s.store.Resources.DeleteDatabase(r.Context(), id); err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) startDatabase(w http.ResponseWriter, r *http.Request) {
	s.databaseAction(w, r, "start")
}

func (s *Server) stopDatabase(w http.ResponseWriter, r *http.Request) {
	s.databaseAction(w, r, "stop")
}

func (s *Server) databaseAction(w http.ResponseWriter, r *http.Request, action string) {
	id := chi.URLParam(r, "id")
	database, err := s.store.Resources.DatabaseByID(r.Context(), id)
	if err != nil {
		mapError(w, err)
		return
	}
	if s.docker == nil {
		writeError(w, http.StatusServiceUnavailable, "no docker engine configured")
		return
	}

	container, err := s.docker.ContainerByName(r.Context(), database.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if container == nil {
		if action == "stop" {
			s.store.Resources.PatchDatabaseStatus(r.Context(), id, db.StatusStopped)
			writeJSON(w, http.StatusOK, map[string]string{"status": db.StatusStopped})
			return
		}
		// A prune or manual cleanup removed the container. Recreate it from the
		// stored definition: the data volume and the sealed root password survive.
		defaults := databaseSpec(database.Kind, database.Version)
		imageRef := defaults.Image + ":" + defaults.Version
		if err := s.docker.PullImage(r.Context(), imageRef, io.Discard); err != nil {
			writeError(w, http.StatusInternalServerError, "pull image: "+err.Error())
			return
		}

		rootPassword := ""
		if vars, verr := s.store.Applications.EnvVarsByTarget(r.Context(), db.TargetDatabase, id); verr == nil {
			for _, variable := range vars {
				if variable.Key == "ROOT_PASSWORD" {
					if plain, oerr := s.auth.Open(variable.ValueEnc); oerr == nil {
						rootPassword = plain
					}
				}
			}
		}

		containerID, cerr := s.docker.CreateContainer(r.Context(), dockerSpec{
			Name:    database.Name,
			Image:   imageRef,
			Env:     databaseEnv(rootPassword),
			Labels:  map[string]string{"meghify.resource": "database", "meghify.database": id},
			Ports:   map[int]int{defaults.Port: database.PublicPort},
			Restart: "unless-stopped",
			Volumes: []mountSpec{{Source: "meghify-db-" + database.ID, Target: defaults.DataPath}},
		}.toDocker())
		if cerr != nil {
			writeError(w, http.StatusInternalServerError, cerr.Error())
			return
		}
		if serr := s.docker.StartContainer(r.Context(), containerID); serr != nil {
			writeError(w, http.StatusInternalServerError, serr.Error())
			return
		}
		s.store.Resources.PatchDatabaseStatus(r.Context(), id, db.StatusRunning)
		writeJSON(w, http.StatusOK, map[string]string{"status": db.StatusRunning})
		return
	}

	if action == "start" {
		err = s.docker.StartContainer(r.Context(), container.ID)
	} else {
		err = s.docker.StopContainer(r.Context(), container.ID, 20*time.Second)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	status := db.StatusStopped
	if action == "start" {
		status = db.StatusRunning
	}
	s.store.Resources.PatchDatabaseStatus(r.Context(), id, status)
	writeJSON(w, http.StatusOK, map[string]string{"status": status})
}

// --- backups ---

func (s *Server) listBackups(w http.ResponseWriter, r *http.Request) {
	backups, err := s.store.Resources.BackupsByTarget(r.Context(), db.TargetDatabase, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if backups == nil {
		backups = []*db.Backup{}
	}
	writeJSON(w, http.StatusOK, backups)
}

type backupRequest struct {
	Schedule string `json:"schedule"`
}

func (s *Server) createBackup(w http.ResponseWriter, r *http.Request) {
	databaseID := chi.URLParam(r, "id")
	var req backupRequest
	if r.ContentLength > 0 {
		if err := decode(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	backup := &db.Backup{
		ID:       db.NewID(),
		Target:   db.TargetDatabase,
		TargetID: databaseID,
		Status:   "pending",
		Schedule: strings.TrimSpace(req.Schedule),
	}
	if err := s.store.Resources.CreateBackup(r.Context(), backup); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	payload := `{"backupId":"` + backup.ID + `"}`
	if req.Schedule != "" {
		if _, err := s.store.Jobs.Enqueue(r.Context(), db.JobBackup, payload, time.Now(), 1); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusCreated, backup)
}

func (s *Server) deleteBackup(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Resources.DeleteBackup(r.Context(), chi.URLParam(r, "backupID")); err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
