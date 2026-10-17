// Package config loads instance configuration from the environment.
//
// Everything is env-driven so the panel can be shipped as a single static
// binary with no config file to parse at runtime.
package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/caarlos0/env/v11"
)

// Dialect is the active database driver.
type Dialect string

const (
	DialectSQLite   Dialect = "sqlite"
	DialectPostgres Dialect = "postgres"
)

// String returns the driver name, which doubles as the database/sql driver name.
func (d Dialect) String() string { return string(d) }

// Config is the resolved instance configuration.
type Config struct {
	Env  string `env:"APP_ENV" envDefault:"production"`
	Addr string `env:"PANEL_ADDR" envDefault:"0.0.0.0:8080"`

	// PublicURL is the externally reachable base URL of the panel. It is used
	// to build absolute links in emails and webhooks.
	PublicURL string `env:"PANEL_PUBLIC_URL" envDefault:"http://localhost:8080"`

	Dialect Dialect `env:"DB_DRIVER" envDefault:"sqlite"`
	// DSN is either a file path (sqlite) or a postgres connection string.
	//
	// Defaults to a file inside DataDir so setting DATA_DIR alone moves the whole
	// store, which is what operators expect from a self-hosted panel.
	DSN string `env:"DB_DSN"`

	// PostgresURL is only consulted when Dialect is postgres.
	PostgresURL string `env:"DATABASE_URL"`

	DataDir string `env:"DATA_DIR" envDefault:"/data"`

	// AuthSecret encrypts secrets at rest (env vars, ssh keys, TOTP seeds).
	// 32 bytes of hex. Generated on first boot when empty.
	AuthSecret string `env:"AUTH_SECRET"`

	SessionTTL     time.Duration `env:"SESSION_TTL" envDefault:"720h"`
	APITokenTTL    time.Duration `env:"API_TOKEN_TTL" envDefault:"8760h"`
	PasswordMinLen int           `env:"PASSWORD_MIN_LEN" envDefault:"10"`
	// AllowSignup lets the first user self-register. Disabled after that.
	AllowSignup bool `env:"ALLOW_SIGNUP" envDefault:"false"`

	DockerHost string `env:"DOCKER_HOST" envDefault:"unix:///var/run/docker.sock"`

	// BuildHelperImage overrides the build helper image. Empty picks the
	// image published for this exact panel release
	// (ghcr.io/tamalmaity-dev/meghify-helper:<version>).
	BuildHelperImage string `env:"BUILD_HELPER_IMAGE"`

	// BuildConcurrency caps how many deployments may build at the same time.
	// Builds are CPU-heavy; one at a time keeps a small server responsive.
	BuildConcurrency int `env:"BUILD_CONCURRENCY" envDefault:"1"`

	// BuildCPULimit caps a build helper container's CPU (fraction of one
	// core); zero keeps the engine default. BuildMemoryLimit accepts bytes or
	// a suffix like 512m / 2g; empty keeps the engine default.
	BuildCPULimit    float64 `env:"BUILD_CPU_LIMIT" envDefault:"0"`
	BuildMemoryLimit string  `env:"BUILD_MEMORY_LIMIT"`

	// ProxyImage is the reverse proxy (Traefik) the panel deploys on demand
	// for applications with domains. Empty disables proxy management. Traefik
	// v3.6 or newer is required: older releases default to Docker API 1.24,
	// which Engine 29+ refuses.
	ProxyImage string `env:"PROXY_IMAGE" envDefault:"traefik:v3.7"`
	// ProxyHTTPPort and ProxyHTTPSPort are the host ports the proxy binds.
	ProxyHTTPPort  int `env:"PROXY_HTTP_PORT" envDefault:"80"`
	ProxyHTTPSPort int `env:"PROXY_HTTPS_PORT" envDefault:"443"`
	// ACMEEmail registers the Let's Encrypt account used for TLS certificates.
	ACMEEmail string `env:"ACME_EMAIL"`

	// Registry is where built application images are pushed.
	RegistryURL  string `env:"REGISTRY_URL" envDefault:""`
	RegistryUser string `env:"REGISTRY_USER" envDefault:""`
	RegistryPass string `env:"REGISTRY_PASS" envDefault:""`

	// Workers sizing for the durable job runner.
	WorkerConcurrency int `env:"WORKER_CONCURRENCY" envDefault:"4"`
	JobMaxAttempts    int `env:"JOB_MAX_ATTEMPTS" envDefault:"3"`

	// MetricsRetentionDays controls how long container metrics are kept.
	MetricsRetentionDays int `env:"METRICS_RETENTION_DAYS" envDefault:"14"`

	S3Endpoint  string `env:"S3_ENDPOINT" envDefault:""`
	S3Bucket    string `env:"S3_BUCKET" envDefault:""`
	S3AccessKey string `env:"S3_ACCESS_KEY" envDefault:""`
	S3SecretKey string `env:"S3_SECRET_KEY" envDefault:""`
	S3Region    string `env:"S3_REGION" envDefault:"us-east-1"`
}

// Load reads configuration from the process environment.
func Load() (*Config, error) {
	var c Config
	if err := env.Parse(&c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	c.applyDefaults()
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// applyDefaults fills in values that depend on other settings, which env parsing
// cannot express.
func (c *Config) applyDefaults() {
	if c.DataDir == "" {
		c.DataDir = "/data"
	}
	if c.Dialect == DialectSQLite && c.DSN == "" {
		c.DSN = filepath.Join(c.DataDir, "meghify.db")
	}
	if c.Dialect == DialectPostgres && c.DSN == "" {
		c.DSN = c.PostgresURL
	}
}

func (c *Config) validate() error {
	switch c.Dialect {
	case DialectSQLite, DialectPostgres:
	default:
		return fmt.Errorf("unsupported DB_DRIVER %q (want %q or %q)", c.Dialect, DialectSQLite, DialectPostgres)
	}

	if c.Dialect == DialectPostgres && c.DSN == "" {
		return errors.New("DATABASE_URL is required when DB_DRIVER=postgres")
	}
	if c.Dialect == DialectSQLite && c.DSN == "" {
		return errors.New("DB_DSN is required when DB_DRIVER=sqlite")
	}
	if c.SessionTTL <= 0 {
		return errors.New("SESSION_TTL must be positive")
	}
	if c.PasswordMinLen < 8 {
		return errors.New("PASSWORD_MIN_LEN must be at least 8")
	}
	if c.WorkerConcurrency <= 0 {
		return errors.New("WORKER_CONCURRENCY must be at least 1")
	}
	if c.JobMaxAttempts <= 0 {
		return errors.New("JOB_MAX_ATTEMPTS must be at least 1")
	}
	if c.BuildConcurrency < 1 {
		return errors.New("BUILD_CONCURRENCY must be at least 1")
	}
	return nil
}

// IsProduction reports whether the panel runs in production mode.
func (c *Config) IsProduction() bool { return c.Env == "production" }
