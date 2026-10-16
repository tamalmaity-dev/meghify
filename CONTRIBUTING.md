# Contributing

Thanks for helping. This project exists because running a self-hosted PaaS should
not cost you most of a VPS, so keep that constraint in mind when adding
dependencies or background work.

## Ground rules

- **Every change must stay inside the resource budget.** If a change adds a
  long-running goroutine, a new container, or a runtime dependency to the request
  path, say so in the pull request and show the measurement
  (`make measure`).
- **Do not add a cache service.** The queue is a table, the realtime layer is
  SSE. If you need Redis, the design needs revisiting first.
- **Prefer stdlib.** `net/http`, `log/slog`, `database/sql`, and `io/fs` cover
  more than people expect.
- **Comment the why, not the what.** No section-divider comments, no comments
  restating the code.
- **Keep the panel a single binary in production.** Frontend output is embedded
  with `//go:embed`; do not introduce a static file server.

## Getting set up

```bash
git clone https://github.com/tamalmaity-dev/meghify.git
cd meghify
go test ./...
make web
make run
```

## Adding an endpoint

1. Add the handler in `internal/api/`.
2. Add the route in `Router()`.
3. Add the repository method in `internal/db/` needed by it.
4. Write a test if it touches the queue, timestamps, or a nullable column.
5. Add the TypeScript call in `web/src/lib/api.ts` and the UI.

## Adding a migration

Migrations are plain SQL applied at boot. Write the same change twice, once per
dialect, in `internal/db/migrations/`:

```
NNN_description.sqlite.sql
NNN_description.postgres.sql
```

Conventions that keep both dialects identical:

- Primary keys are UUID text generated in Go. No sequences.
- Timestamps are written as RFC3339Nano UTC text; `db.Text` and `db.Parse` do
  the conversion.
- Nullable columns must be scanned as `sql.NullString`, never `string`. This has
  caused real runtime failures; the tests in `internal/db` cover the cases.
- Booleans are `INTEGER` on sqlite and `BOOLEAN` on postgres.

## Migrations must be reversible

A migration file is never edited after it ships. To change a column, add a new
migration. Existing installations apply the new file on next boot.

## Tests

```bash
go test ./...          # Go tests
make vet               # go vet plus the frontend typecheck
node scripts/smoke.mjs # needs a running panel
```

The Go tests that matter most are in `internal/db/store_test.go`: queue claim
semantics, retry backoff, dead-lettering, nullable columns, and secret storage.
Break those and the panel fails at runtime in ways the happy path will not show.

## Reporting a bug

Include: your panel version, OS and architecture, Docker version, and the panel
log output. Resource reports should state whether the panel was measured idle or
under a deploy.
