// Package deploy runs the build and release pipeline for an application.
//
// A deployment is a single durable job. The pipeline streams progress to a log
// file (so history survives restarts) and to the realtime hub (so the UI updates
// live) through one io.Writer, keeping the two concerns decoupled.
package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tamalmaity-dev/meghify/internal/db"
	"github.com/tamalmaity-dev/meghify/internal/docker"
)

// SecretBox seals and opens stored secrets.
type SecretBox interface {
	Encrypt(string) (string, error)
	Decrypt(string) (string, error)
}

// Emitter broadcasts deployment progress to connected browsers.
type Emitter interface {
	BroadcastDeployment(deploymentID string, payload any)
}

// RegistryConfig is where built images are pushed.
type RegistryConfig struct {
	URL  string
	User string
	Pass string
}

// Options configures the pipeline.
type Options struct {
	Store    *db.Store
	Secrets  SecretBox
	Client   *docker.Client
	DataDir  string
	Registry RegistryConfig
	Emitter  Emitter
	// GitToken resolves a stored credential for a private repository. Optional.
	GitToken GitCredentialResolver
}

// Payload is the job body carried by a deploy job.
type Payload struct {
	DeploymentID  string `json:"deploymentId"`
	ApplicationID string `json:"applicationId"`
	NoCache       bool   `json:"noCache"`
}

// Pipeline executes deployments.
type Pipeline struct {
	store    *db.Store
	secrets  SecretBox
	client   *docker.Client
	dataDir  string
	registry RegistryConfig
	emitter  Emitter
	git      GitCredentialResolver

	mu      sync.Mutex
	running map[string]struct{}
}

// NewPipeline builds a pipeline.
func NewPipeline(opts Options) *Pipeline {
	return &Pipeline{
		store:    opts.Store,
		secrets:  opts.Secrets,
		client:   opts.Client,
		dataDir:  opts.DataDir,
		registry: opts.Registry,
		emitter:  opts.Emitter,
		git:      opts.GitToken,
		running:  map[string]struct{}{},
	}
}

// Run executes one deployment. Concurrent calls for the same deployment are
// ignored so a webhook storm cannot double-build.
func (p *Pipeline) Run(ctx context.Context, payload Payload) error {
	p.mu.Lock()
	if _, busy := p.running[payload.DeploymentID]; busy {
		p.mu.Unlock()
		return nil
	}
	p.running[payload.DeploymentID] = struct{}{}
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.running, payload.DeploymentID)
		p.mu.Unlock()
	}()

	deployment, err := p.store.Deployments.DeploymentByID(ctx, payload.DeploymentID)
	if err != nil {
		return err
	}
	if deployment.Status == db.DeploySuccess || deployment.Status == db.DeployCanceled {
		return nil
	}

	app, err := p.store.Applications.ApplicationByID(ctx, deployment.ApplicationID)
	if err != nil {
		return err
	}

	// The log file is durable so a deployment's output survives a panel restart
	// and can be replayed from the history view.
	logPath := filepath.Join(p.dataDir, "deployments", deployment.ID+".log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return err
	}
	logFile, err := os.Create(logPath)
	if err != nil {
		return err
	}
	defer logFile.Close()
	if err := p.store.Deployments.SetDeploymentLogPath(ctx, deployment.ID, logPath); err != nil {
		return err
	}

	writer := newTeeWriter(logFile, p.emitter, deployment.ID)
	p.store.Deployments.StartDeployment(ctx, deployment.ID, "")
	p.store.Applications.PatchApplicationStatus(ctx, app.ID, db.StatusDeploying)
	p.emit(app.ID, deployment.ID, "started", "")

	started := time.Now()
	runErr := p.execute(ctx, writer, app, payload)

	elapsed := time.Since(started).Round(time.Millisecond)
	if runErr != nil {
		writer.Write([]byte(fmt.Sprintf("\n[x] deployment failed after %s: %s\n", elapsed, runErr.Error())))
		p.store.Deployments.FinishDeployment(ctx, deployment.ID, db.DeployFailed, runErr.Error())
		p.store.Applications.PatchApplicationStatus(ctx, app.ID, db.StatusError)
		p.emit(app.ID, deployment.ID, "failed", runErr.Error())
		return runErr
	}

	writer.Write([]byte(fmt.Sprintf("\n[ok] deployment finished in %s\n", elapsed)))
	p.store.Deployments.FinishDeployment(ctx, deployment.ID, db.DeploySuccess, "")
	p.store.Applications.PatchApplicationStatus(ctx, app.ID, db.StatusRunning)
	p.emit(app.ID, deployment.ID, "success", "")
	return nil
}

// execute builds and releases one application.
func (p *Pipeline) execute(ctx context.Context, sink io.Writer, app *db.Application, payload Payload) error {
	if p.client == nil {
		return errors.New("no docker engine configured for the target node")
	}
	if err := p.client.Ping(ctx); err != nil {
		return fmt.Errorf("docker engine unreachable: %w", err)
	}

	switch app.BuildPack {
	case db.BuildImage:
		return p.runImage(ctx, sink, app)
	case db.BuildCompose:
		return p.runCompose(ctx, sink, app)
	case db.BuildDockerfile:
		return p.runDockerfile(ctx, sink, app, payload)
	case db.BuildNixpacks, db.BuildRailpack:
		return p.runAutoBuild(ctx, sink, app, payload)
	default:
		return fmt.Errorf("unsupported build pack %q", app.BuildPack)
	}
}

func (p *Pipeline) emit(appID, deploymentID, event, message string) {
	if p.emitter == nil {
		return
	}
	p.emitter.BroadcastDeployment(deploymentID, map[string]any{
		"applicationId": appID,
		"deploymentId":  deploymentID,
		"event":         event,
		"message":       message,
	})
}

func sanitizeName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "app"
	}
	return out
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func networkName(app *db.Application) string {
	return "meghify-" + sanitizeName(app.Name)
}
