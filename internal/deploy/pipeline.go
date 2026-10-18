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
	// GitToken mints a credential for an application that clones private
	// repositories. Optional: public repositories clone anonymously.
	GitToken GitCredentialResolver
	// HelperImage is the container image that runs git, buildpack and compose
	// commands on the target engine. Empty disables the helper path.
	HelperImage string
	// HelperSocket is the docker socket path mounted into the helper.
	HelperSocket string
	// ProxyImage is the reverse proxy (Traefik) deployed on demand when an
	// application has domains. Empty disables proxy management.
	ProxyImage string
	// ProxyHTTPPort and ProxyHTTPSPort are the host ports the proxy binds;
	// zero falls back to 80/443.
	ProxyHTTPPort  int
	ProxyHTTPSPort int
	// ProxyEmail registers a Let's Encrypt account for TLS certificates.
	ProxyEmail string
	// BuildConcurrency caps concurrent builds; values below 1 mean 1.
	BuildConcurrency int
	// BuildCPULimit and BuildMemoryLimit bound the build sandbox. Zero values
	// mean "half of this node" (see sandboxLimits).
	BuildCPULimit    float64
	BuildMemoryLimit int64
	// SandboxMode is auto, on or off; sandboxImage is the Docker-in-Docker
	// image the isolated build daemon runs from.
	SandboxMode  string
	SandboxImage string
	// DeployTimeout bounds one whole deployment; zero leaves it unbounded.
	DeployTimeout time.Duration
	// ImageKeep is how many built images per application survive a cleanup:
	// the newest ones. The running container's image always survives.
	ImageKeep int
	// BuildCacheKeep bounds the build cache in bytes after every release and
	// on cleanup passes; zero leaves it unbounded.
	BuildCacheKeep int64
	// DefaultNodeVersion is passed to nixpacks when the application does not
	// set NIXPACKS_NODE_VERSION itself.
	DefaultNodeVersion string
}

// Payload is the job body carried by a deploy job.
type Payload struct {
	DeploymentID  string `json:"deploymentId"`
	ApplicationID string `json:"applicationId"`
	NoCache       bool   `json:"noCache"`
}

// Pipeline executes deployments.
type Pipeline struct {
	store        *db.Store
	secrets      SecretBox
	client       *docker.Client
	dataDir      string
	registry     RegistryConfig
	emitter      Emitter
	git          GitCredentialResolver
	helperImage  string
	helperSocket string
	proxyImage   string
	proxyEmail   string
	proxyPort    int
	proxyTLSPort int

	// buildGate serialises builds so a small server finishes one deployment
	// before the next begins.
	buildGate        chan struct{}
	buildCPULimit    float64
	buildMemoryLimit int64

	// sandboxMode is auto, on or off; sandboxImage defaults to Docker-in-Docker.
	sandboxMode  string
	sandboxImage string
	// deployTimeout bounds one whole deployment.
	deployTimeout time.Duration
	// imageKeep drives the post-release image trim: keep N newest images per
	// application repository. Zero falls back to 2.
	imageKeep int
	// buildCacheKeep drives the post-release cache trim; zero leaves the
	// cache unbounded.
	buildCacheKeep int64
	// defaultNodeVersion keeps nixpacks off its end-of-life Node 18 fallback.
	defaultNodeVersion string

	mu      sync.Mutex
	running map[string]struct{}
	// cancel holds the cancel function of an in-flight deployment; helpers
	// tracks the build helper containers it created so a cancellation can
	// take them down immediately.
	cancel  map[string]context.CancelFunc
	helpers map[string]map[string]struct{}
}

// NewPipeline builds a pipeline.
func NewPipeline(opts Options) *Pipeline {
	concurrency := opts.BuildConcurrency
	if concurrency < 1 {
		concurrency = 1
	}
	sandboxMode := opts.SandboxMode
	if sandboxMode == "" {
		sandboxMode = "auto"
	}
	return &Pipeline{
		store:              opts.Store,
		secrets:            opts.Secrets,
		client:             opts.Client,
		dataDir:            opts.DataDir,
		registry:           opts.Registry,
		emitter:            opts.Emitter,
		git:                opts.GitToken,
		helperImage:        opts.HelperImage,
		helperSocket:       opts.HelperSocket,
		proxyImage:         opts.ProxyImage,
		proxyEmail:         opts.ProxyEmail,
		proxyPort:          opts.ProxyHTTPPort,
		proxyTLSPort:       opts.ProxyHTTPSPort,
		buildGate:          make(chan struct{}, concurrency),
		buildCPULimit:      opts.BuildCPULimit,
		buildMemoryLimit:   opts.BuildMemoryLimit,
		sandboxMode:        sandboxMode,
		sandboxImage:       opts.SandboxImage,
		deployTimeout:      opts.DeployTimeout,
		imageKeep:          opts.ImageKeep,
		buildCacheKeep:     opts.BuildCacheKeep,
		defaultNodeVersion: opts.DefaultNodeVersion,
		running:            map[string]struct{}{},
		cancel:             map[string]context.CancelFunc{},
		helpers:            map[string]map[string]struct{}{},
	}
}

// registerHelper records a build helper container against its deployment.
func (p *Pipeline) registerHelper(deploymentID, containerID string) {
	if deploymentID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	set := p.helpers[deploymentID]
	if set == nil {
		set = map[string]struct{}{}
		p.helpers[deploymentID] = set
	}
	set[containerID] = struct{}{}
}

// unregisterHelper forgets a container that was already torn down.
func (p *Pipeline) unregisterHelper(deploymentID, containerID string) {
	if deploymentID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if set := p.helpers[deploymentID]; set != nil {
		delete(set, containerID)
		if len(set) == 0 {
			delete(p.helpers, deploymentID)
		}
	}
}

// removeHelpers force-removes every helper container of a deployment. It is
// the hard stop behind a cancellation and a build timeout.
func (p *Pipeline) removeHelpers(ctx context.Context, deploymentID string) {
	p.mu.Lock()
	ids := make([]string, 0, len(p.helpers[deploymentID]))
	for id := range p.helpers[deploymentID] {
		ids = append(ids, id)
	}
	delete(p.helpers, deploymentID)
	p.mu.Unlock()

	for _, id := range ids {
		_ = p.client.StopContainer(ctx, id, 5*time.Second)
		_ = p.client.RemoveContainer(ctx, id)
	}
}

// Cancel stops an in-flight deployment: the build context is canceled and the
// helper container is removed, which kills the buildpack process with it.
func (p *Pipeline) Cancel(deploymentID string) bool {
	p.mu.Lock()
	cancel := p.cancel[deploymentID]
	p.mu.Unlock()
	if cancel == nil {
		return false
	}
	cleanupCtx, done := context.WithTimeout(context.Background(), 30*time.Second)
	defer done()
	p.removeHelpers(cleanupCtx, deploymentID)
	cancel()
	return true
}

// Run executes one deployment. Concurrent calls for the same deployment are
// ignored so a webhook storm cannot double-build.
//
// The deployment runs under a deadline (DEPLOY_TIMEOUT): a stuck build is
// stopped, its helper container removed, and the deployment fails with a clear
// message instead of holding the queue forever. Terminal database writes use a
// fresh context because the deployment context may already be canceled.
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
		delete(p.cancel, payload.DeploymentID)
		delete(p.helpers, payload.DeploymentID)
		p.mu.Unlock()
	}()

	deployment, err := p.store.Deployments.DeploymentByID(ctx, payload.DeploymentID)
	if err != nil {
		return err
	}
	// Only pending phases execute; a canceled or finished deployment is never
	// restarted (a stale queue entry from an interrupted panel, for example).
	switch deployment.Status {
	case db.DeployQueued, db.DeployBuilding, db.DeployPushing, db.DeployDeploying:
	default:
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

	// The deployment deadline covers queue wait, build, transfer, release and
	// health checks. Cancellation (API or timeout) flows into every step.
	runCtx := ctx
	if p.deployTimeout > 0 {
		var timeoutCancel context.CancelFunc
		runCtx, timeoutCancel = context.WithTimeout(ctx, p.deployTimeout)
		defer timeoutCancel()
	}
	runCtx, cancelRun := context.WithCancel(runCtx)
	defer cancelRun()
	p.mu.Lock()
	p.cancel[deployment.ID] = cancelRun
	p.mu.Unlock()

	// Safety net: whatever path this deployment takes, its helper containers
	// are gone before the function returns. A fresh context is used because
	// the run context may already be canceled.
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		p.removeHelpers(cleanupCtx, deployment.ID)
	}()

	// One build at a time by default: builds are CPU-heavy, and a small
	// server should digest one before starting the next.
	select {
	case p.buildGate <- struct{}{}:
	default:
		writer.Write([]byte("[*] waiting for another build to finish\n"))
		select {
		case p.buildGate <- struct{}{}:
		case <-runCtx.Done():
			return runCtx.Err()
		}
	}
	defer func() { <-p.buildGate }()

	if err := p.store.Deployments.StartDeployment(runCtx, deployment.ID, ""); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			// Canceled between claim and start.
			writer.Write([]byte("[x] deployment canceled before it started\n"))
			return nil
		}
		return err
	}
	p.store.Applications.PatchApplicationStatus(runCtx, app.ID, db.StatusDeploying)
	p.emit(app.ID, deployment.ID, "started", "")
	if p.deployTimeout > 0 {
		writer.Write([]byte("[*] deployment timeout: " + p.deployTimeout.String() + " (raise DEPLOY_TIMEOUT for long builds)\n"))
	}

	started := time.Now()
	runErr := p.execute(runCtx, writer, app, payload)
	elapsed := time.Since(started).Round(time.Millisecond)

	// Final writes need their own deadline: the run context may already be
	// canceled (a kill or the deploy timeout), and a context created at the
	// top of the run would be expired by the time a long build fails.
	finalCtx, finalCancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer finalCancel()

	if runErr != nil {
		// A cancellation (user or timeout) leaves the terminal state to the
		// path that requested it.
		current, _ := p.store.Deployments.DeploymentByID(finalCtx, deployment.ID)
		if current != nil && !isPendingStatus(current.Status) {
			if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
				writer.Write([]byte("\n[x] deployment canceled after " + elapsed.String() + "\n"))
			}
			p.cleanupTempContainers(finalCtx, app)
			return runErr
		}

		message := runErr.Error()
		if errors.Is(runErr, context.DeadlineExceeded) {
			message = fmt.Sprintf("deployment timed out after %s and was stopped; check the log for a hung step, then retry (raise DEPLOY_TIMEOUT to allow longer builds)", p.deployTimeout)
		} else if errors.Is(runErr, context.Canceled) {
			message = "deployment canceled"
		}
		writer.Write([]byte(fmt.Sprintf("\n[x] deployment failed after %s: %s\n", elapsed, message)))
		p.store.Deployments.FinishDeployment(finalCtx, deployment.ID, db.DeployFailed, message)
		p.store.Applications.PatchApplicationStatus(finalCtx, app.ID, db.StatusError)
		p.emit(app.ID, deployment.ID, "failed", message)
		p.cleanupTempContainers(finalCtx, app)
		return errors.New(message)
	}

	// A cancel that raced the final step must not be overwritten by success.
	if current, _ := p.store.Deployments.DeploymentByID(finalCtx, deployment.ID); current != nil && current.Status == db.DeployCanceled {
		writer.Write([]byte("\n[x] deployment canceled after " + elapsed.String() + "\n"))
		p.cleanupTempContainers(finalCtx, app)
		return nil
	}

	writer.Write([]byte(fmt.Sprintf("\n[ok] deployment finished in %s\n", elapsed)))
	p.store.Deployments.FinishDeployment(finalCtx, deployment.ID, db.DeploySuccess, "")
	p.store.Applications.PatchApplicationStatus(finalCtx, app.ID, db.StatusRunning)
	p.emit(app.ID, deployment.ID, "success", "")
	return nil
}

// isPendingStatus reports whether a deployment still has work to do.
func isPendingStatus(status string) bool {
	switch status {
	case db.DeployQueued, db.DeployBuilding, db.DeployPushing, db.DeployDeploying:
		return true
	}
	return false
}

// cleanupTempContainers removes rolling-release leftovers of an application:
// containers labeled for it that do not carry its final name. They only exist
// when a deployment died between create and rename.
func (p *Pipeline) cleanupTempContainers(ctx context.Context, app *db.Application) {
	if p.client == nil || app == nil {
		return
	}
	cleanupTempContainers(ctx, p.client, app)
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
		return p.runCompose(ctx, sink, app, payload)
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

// NetworkName is the private network an application's container joins.
func NetworkName(app *db.Application) string { return networkName(app) }

func networkName(app *db.Application) string {
	return "meghify-" + sanitizeName(app.Name)
}
