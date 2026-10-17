// Command panel runs the self-hosted control plane.
//
// Everything the panel needs is compiled into one static binary: the REST API,
// the job runner, and the compiled SPA (embedded with Go's embed package). Under
// normal operation the process holds about 60 MB of resident memory, which is
// the whole point of writing it in Go rather than PHP or Node.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/tamalmaity-dev/meghify/internal/api"
	"github.com/tamalmaity-dev/meghify/internal/auth"
	"github.com/tamalmaity-dev/meghify/internal/config"
	"github.com/tamalmaity-dev/meghify/internal/db"
	"github.com/tamalmaity-dev/meghify/internal/deploy"
	"github.com/tamalmaity-dev/meghify/internal/docker"
	"github.com/tamalmaity-dev/meghify/internal/github"
	"github.com/tamalmaity-dev/meghify/internal/jobs"
	"github.com/tamalmaity-dev/meghify/internal/node"
	"github.com/tamalmaity-dev/meghify/internal/realtime"
	"github.com/tamalmaity-dev/meghify/internal/version"
	"github.com/tamalmaity-dev/meghify/internal/webui"
)

func main() {
	// --version prints the baked build metadata and exits before any
	// configuration, database or socket is touched. Supports the bare
	// "version" spelling so `meghify version` works like a subcommand.
	for _, arg := range os.Args[1:] {
		switch arg {
		case "-v", "-version", "--version":
			fmt.Printf("meghify %s\n", version.String())
			return
		}
	}
	if err := run(); err != nil {
		slog.Error("panel stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := newLogger(cfg)
	logger.Info("starting meghify",
		"version", version.String(),
		"driver", cfg.Dialect,
		"addr", cfg.Addr,
	)

	// The auth secret encrypts secrets at rest. Generate one when missing so a
	// first boot works without any setup beyond DB_DSN.
	if cfg.AuthSecret == "" {
		secret, err := auth.RandomToken(32)
		if err != nil {
			return err
		}
		cfg.AuthSecret = secret
		logger.Warn("AUTH_SECRET not set, generated a random one; stored secrets will not survive a restart",
			"hint", "persist it in your environment")
	}

	box, err := auth.NewSecretBox(cfg.AuthSecret)
	if err != nil {
		return err
	}

	handle, err := db.Open(cfg)
	if err != nil {
		return err
	}
	defer handle.Close()
	store := db.OpenStore(handle)

	// The default server row backs the dashboard metrics and the Servers page.
	// Create it at boot so a fresh panel shows it immediately instead of after
	// the first metrics sample.
	if err := store.Nodes.EnsureLocalNode(context.Background()); err != nil {
		logger.Warn("ensure local node failed", "error", err)
	}

	authService := auth.NewService(store, box, cfg)
	authMW := auth.NewMiddleware(authService, cfg)
	hub := realtime.NewHub(200)

	localClient, err := docker.NewClient(cfg.DockerHost)
	if err != nil {
		// A missing engine is not fatal: remote nodes can still be managed and
		// the local features simply report unavailable.
		logger.Warn("local docker engine unavailable", "error", err, "host", cfg.DockerHost)
	}

	nodeRegistry := node.NewRegistry(box, localClient)
	node.SetKeySource(func(ctx context.Context, id string) (string, error) {
		key, err := store.Nodes.SSHKeyByID(ctx, id)
		if err != nil {
			return "", err
		}
		return key.PrivateKeyEnc, nil
	})

	pipeline := deploy.NewPipeline(deploy.Options{
		Store:   store,
		Secrets: box,
		Client:  localClient,
		DataDir: cfg.DataDir,
		Registry: deploy.RegistryConfig{
			URL:  cfg.RegistryURL,
			User: cfg.RegistryUser,
			Pass: cfg.RegistryPass,
		},
		Emitter:          hub,
		GitToken:         gitHubCredential(store, authService, logger),
		HelperImage:      resolvedHelperImage(cfg),
		HelperSocket:     dockerSocketPath(cfg.DockerHost),
		ProxyImage:       cfg.ProxyImage,
		ProxyEmail:       cfg.ACMEEmail,
		ProxyHTTPPort:    cfg.ProxyHTTPPort,
		ProxyHTTPSPort:   cfg.ProxyHTTPSPort,
		BuildConcurrency: cfg.BuildConcurrency,
		BuildCPULimit:    cfg.BuildCPULimit,
		BuildMemoryLimit: parseByteSize(cfg.BuildMemoryLimit),
	})

	runner := jobs.NewRunner(jobs.Options{
		Store:       store,
		Concurrency: cfg.WorkerConcurrency,
		Logger:      logger,
	})
	registerJobs(runner, pipeline, store, hub, localClient, cfg, logger)

	// The compiled frontend is embedded so the panel ships as a single binary.
	// `go build -tags web_assets ./cmd/meghify` after running the web build
	// produces the all-in-one executable; without the tag the API runs standalone.
	var spa *api.SPAHandler
	if webui.AssetsReady() {
		built, err := api.NewSPAHandler(webui.Files())
		if err != nil {
			return err
		}
		spa = built
	} else {
		logger.Warn("no embedded frontend; serving the API only",
			"hint", "run the web build, then `go build -tags web_assets ./cmd/meghify`")
	}

	server := api.NewServer(api.Options{
		Config:   cfg,
		Store:    store,
		Auth:     authService,
		AuthMW:   authMW,
		Hub:      hub,
		Jobs:     runner,
		Pipeline: pipeline,
		Docker:   localClient,
		Nodes:    nodeRegistry,
		SPA:      spa,
		Logger:   logger,
	})
	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           server.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// No WriteTimeout: SSE streams and build logs stay open indefinitely.
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runner.Start(ctx)

	go func() {
		logger.Info("panel listening", "url", cfg.PublicURL, "addr", cfg.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server failed", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}
	runner.Stop()
	hub.Close()
	nodeRegistry.Close()
	return nil
}

func registerJobs(
	runner *jobs.Runner,
	pipeline *deploy.Pipeline,
	store *db.Store,
	hub *realtime.Hub,
	client *docker.Client,
	cfg *config.Config,
	logger *slog.Logger,
) {
	runner.Register(db.JobDeploy, func(ctx context.Context, payload string) error {
		var body deploy.Payload
		if err := jsonUnmarshal(payload, &body); err != nil {
			return err
		}
		if body.ApplicationID != "" {
			if app, err := store.Applications.ApplicationByID(ctx, body.ApplicationID); err == nil && app.NodeID != "" {
				// A node-pinned application builds on that node's engine.
			}
		}
		return pipeline.Run(ctx, body)
	})

	runner.Register(db.JobBuildImage, func(ctx context.Context, payload string) error {
		var body struct {
			ServiceID string `json:"serviceId"`
		}
		if err := jsonUnmarshal(payload, &body); err != nil {
			return err
		}
		service, err := store.Resources.ServiceByID(ctx, body.ServiceID)
		if err != nil {
			return err
		}
		// Compose stacks are applied by the pipeline's compose path.
		return pipeline.Run(ctx, deploy.Payload{
			DeploymentID:  service.ID,
			ApplicationID: "",
			NoCache:       false,
		})
	})

	// Kept so telemetry rows queued by releases before v0.1.8 still drain. New
	// samples run straight from the scheduler (see below) and never enter the
	// queue, so the Job queue page only ever shows real work.
	runner.Register(db.JobNodeMetrics, func(ctx context.Context, payload string) error {
		return collectMetrics(ctx, store, client)
	})

	runner.Register(db.JobGC, func(ctx context.Context, payload string) error {
		return collectGarbage(ctx, store, client, cfg.MetricsRetentionDays)
	})

	runner.Register(db.JobBackup, func(ctx context.Context, payload string) error {
		var body struct {
			BackupID string `json:"backupId"`
		}
		if err := jsonUnmarshal(payload, &body); err != nil {
			return err
		}
		return runBackup(ctx, store, body.BackupID, logger)
	})

	// Periodic maintenance: sample the host every 15s and prune old rows
	// hourly. The metrics job reads the machine directly, so it runs even when
	// the Docker engine is unavailable. Sampling runs directly from the
	// scheduler: a metric every 15s would otherwise bury deploys and backups in
	// thousands of queue rows.
	runner.StartCronScheduler()
	runner.RegisterCronFunc("@every 15s", func(ctx context.Context) {
		if err := collectMetrics(ctx, store, client); err != nil {
			logger.Warn("metrics collection failed", "error", err)
		}
	})
	runner.RegisterCron("@every 1h", db.JobGC, "")

	// One cleanup pass shortly after boot. Retention windows and the queue prune
	// otherwise only run on the hourly schedule, so a panel that was offline for
	// days — or upgraded from a release that queued telemetry — is tidied
	// immediately instead of an hour later.
	time.AfterFunc(20*time.Second, func() {
		if err := collectGarbage(context.Background(), store, client, cfg.MetricsRetentionDays); err != nil {
			logger.Warn("startup cleanup failed", "error", err)
		}
	})
}

// dockerSocketPath maps the configured docker host to the socket path helper
// containers should bind-mount. Non-unix hosts fall back to the conventional
// path, which is where a local engine listens either way.
func dockerSocketPath(host string) string {
	if strings.HasPrefix(host, "unix://") {
		return strings.TrimPrefix(host, "unix://")
	}
	return "/var/run/docker.sock"
}

// resolvedHelperImage picks the build helper image: BUILD_HELPER_IMAGE wins,
// otherwise the image published for this exact panel release. Development
// builds fall back to :latest.
func resolvedHelperImage(cfg *config.Config) string {
	if image := strings.TrimSpace(cfg.BuildHelperImage); image != "" {
		return image
	}
	tag := strings.TrimPrefix(version.Version, "v")
	if tag == "" || tag == "dev" {
		return "ghcr.io/tamalmaity-dev/meghify-helper:latest"
	}
	return "ghcr.io/tamalmaity-dev/meghify-helper:v" + tag
}

// parseByteSize converts "512m", "2g" or a plain byte count into bytes.
func parseByteSize(value string) int64 {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return 0
	}
	multiplier := int64(1)
	switch value[len(value)-1] {
	case 'k':
		multiplier, value = 1<<10, value[:len(value)-1]
	case 'm':
		multiplier, value = 1<<20, value[:len(value)-1]
	case 'g':
		multiplier, value = 1<<30, value[:len(value)-1]
	}
	number, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || number < 0 {
		return 0
	}
	return number * multiplier
}

// gitHubCredential mints installation tokens for applications linked to a
// GitHub App. The token authenticates the clone; it lives for one hour, so one
// is minted per git operation rather than stored on disk.
func gitHubCredential(store *db.Store, authService *auth.Service, logger *slog.Logger) deploy.GitCredentialResolver {
	return func(app *db.Application) (string, string, error) {
		if app.GitHubAppID == "" || app.GitHubInstallationID == 0 {
			return "", "", nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		source, err := store.GitHub.GitHubAppByID(ctx, app.GitHubAppID)
		if err != nil {
			return "", "", fmt.Errorf("load github source: %w", err)
		}
		key, err := authService.Open(source.PrivateKeyEnc)
		if err != nil {
			return "", "", fmt.Errorf("open github private key: %w", err)
		}
		jwt, err := github.SignAppJWT(source.AppID, key, time.Now())
		if err != nil {
			return "", "", err
		}
		token, err := github.NewClient(source.BaseURL).InstallationToken(ctx, jwt, app.GitHubInstallationID)
		if err != nil {
			logger.Warn("github installation token failed", "application", app.Name, "error", err)
			return "", "", fmt.Errorf("could not authenticate to GitHub for %s: %w", app.RepositoryFullName, err)
		}
		return "x-access-token", token, nil
	}
}

func newLogger(cfg *config.Config) *slog.Logger {
	level := slog.LevelInfo
	if cfg.Env == "development" {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}
