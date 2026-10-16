// Package db owns the database handle, migrations, and the time encoding that
// keeps the schema portable between sqlite and postgres.
//
// Timestamps are always handled as RFC3339Nano UTC strings at the driver
// boundary. That is the one convention that behaves identically on both
// drivers, so queries never need dialect-specific time scanning.
package db

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tamalmaity-dev/meghify/internal/config"
)

//go:embed migrations/*.sqlite.sql migrations/*.postgres.sql
var migrationFS embed.FS

const migrationTable = "schema_migrations"

// Open connects to the configured database and applies pending migrations.
func Open(cfg *config.Config) (*sql.DB, error) {
	if cfg.Dialect == config.DialectSQLite {
		// The sqlite driver will not create missing parent directories, so a
		// first boot on an empty volume fails without this.
		if dir := filepath.Dir(cfg.DSN); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o750); err != nil {
				return nil, fmt.Errorf("create data dir: %w", err)
			}
		}
	}
	return open(cfg.Dialect, cfg.DSN)
}

// OpenPath connects to a database at an explicit DSN and applies migrations.
// It is used by tests and by operators running the panel outside the config file.
func OpenPath(dsn string) (*sql.DB, error) {
	driver := config.DialectSQLite
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		driver = config.DialectPostgres
	}
	return open(driver, dsn)
}

// sqlitePragmas are applied to every pooled connection through the DSN.
//
// busy_timeout and foreign_keys are per-connection settings: running them once
// on the pool handle configures only whichever connection happened to serve
// the statement, leaving the rest with busy_timeout=0 (instant SQLITE_BUSY)
// and foreign key enforcement off. The driver honors _pragma options on every
// connection it opens.
var sqlitePragmas = []string{
	"busy_timeout(5000)",
	"journal_mode(WAL)",
	"synchronous(NORMAL)",
	"foreign_keys(1)",
	"temp_store(MEMORY)",
	"cache_size(-8000)",
}

// sqliteDSN appends the shared pragmas to a sqlite DSN. Pragmas the caller has
// already configured in the DSN win over the defaults; everything else is
// appended so no connection silently loses foreign key enforcement or the
// busy timeout.
func sqliteDSN(dsn string) string {
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	parts := make([]string, 0, len(sqlitePragmas))
	for _, p := range sqlitePragmas {
		key := p[:strings.IndexByte(p, '(')]
		if strings.Contains(dsn, key+"(") || strings.Contains(dsn, key+"=") || strings.Contains(dsn, key+"%3") {
			continue
		}
		parts = append(parts, "_pragma="+p)
	}
	if len(parts) == 0 {
		return dsn
	}
	return dsn + sep + strings.Join(parts, "&")
}

func open(dialect config.Dialect, dsn string) (*sql.DB, error) {
	if dialect == config.DialectSQLite {
		dsn = sqliteDSN(dsn)
	}
	handle, err := sql.Open(dialect.String(), dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dialect, err)
	}

	// A control plane has a handful of concurrent users. Keeping the pool
	// small is the cheapest way to bound sqlite writer contention.
	handle.SetMaxOpenConns(8)
	handle.SetMaxIdleConns(4)
	handle.SetConnMaxLifetime(time.Hour)

	if err := handle.Ping(); err != nil {
		handle.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}

	if err := migrate(handle, dialect); err != nil {
		handle.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return handle, nil
}

// migrate applies every migration file for the active dialect that has not run.
func migrate(handle *sql.DB, dialect config.Dialect) error {
	if _, err := handle.Exec(fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL)",
		migrationTable,
	)); err != nil {
		return err
	}

	applied, err := appliedVersions(handle)
	if err != nil {
		return err
	}

	suffix := "." + string(dialect) + ".sql"
	entries, err := fs.Glob(migrationFS, "migrations/*"+suffix)
	if err != nil {
		return err
	}
	sort.Strings(entries)

	for _, name := range entries {
		version := strings.TrimPrefix(strings.TrimSuffix(name[len("migrations/"):], suffix), "")
		if applied[version] {
			continue
		}
		script, err := migrationFS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		tx, err := handle.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(script)); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err := tx.Exec(
			fmt.Sprintf("INSERT INTO %s (version, applied_at) VALUES (?, ?)", migrationTable),
			version, Text(time.Now().UTC()),
		); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func appliedVersions(handle *sql.DB) (map[string]bool, error) {
	rows, err := handle.Query(fmt.Sprintf("SELECT version FROM %s", migrationTable))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	applied := map[string]bool{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err()
}
