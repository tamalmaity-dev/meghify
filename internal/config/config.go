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

	// BuildHelperImage is the container image that runs git, buildpack
	// (nixpacks/railpack) and compose commands during a deployment. It needs
	// git, the buildpack binaries and the docker CLI, and it talks to the same
	// engine through the mounted socket. Keeping those tools out of the panel
	// is what lets the panel itself run from a distroless image.
	BuildHelperImage string `env:"BUILD_HELPER_IMAGE" envDefault:"coollabsio/coolify-helper:1.0.17"`

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
	return nil
}

// IsProduction reports whether the panel runs in production mode.
func (c *Config) IsProduction() bool { return c.Env == "production" }
