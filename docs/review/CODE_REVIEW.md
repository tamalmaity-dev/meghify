# Meghify Code Review

**Repository:** `pass-server` / Meghify PaaS panel

**Commit reviewed:** `83ce66b` (`feat(storage): cache bound follows the build; superseded helpers removed`)

**Review date:** 2026-10-20

**Scope:** Go backend, React/Vite frontend, SQL migrations, Docker packaging, installer, configuration.

**Changes made:** None in the reviewed commit. This document is a review; the remediation status below tracks the follow-up branch.

## Remediation status (2026-10-21)

Implemented in the current working tree: C2, C3, C4, H1, H2, H3, H4, H6,
H7, M1–M8, M10, M11, and L1–L14. That includes admin route guards, API-token
scope enforcement, named volumes, repository-relative path validation,
per-node Docker clients, ID-based engine/route names, service compose deploys,
SSH host-key pinning, WebSocket origin checks, login throttling, TOTP sealing,
log retention, and the missing Settings/role-aware UI.

Deferred deliberately: C1 (PostgreSQL), H5 (provisioned-database connectivity)
and M9 (real database backups). These are database-engine features and are
documented as unsupported or incomplete in the README instead of being
advertised as working.

Regression tests now cover repository-relative path validation, API-token
scopes and admin routes, the TOTP login/encryption flow, login throttling, job
crash recovery, SSE overflow/channel validation, deployment-log truncation,
Docker-config naming, and named-volume rendering.

Docker runtime validation (image build, Compose startup, socket group
permissions, remote-node end-to-end deploy) remains unavailable on this
machine; only the static checks above can be re-run here.

---

## Verification performed

| Check | Result |
|---|---|
| `go build ./...` | Pass |
| `go vet ./...` | Pass |
| `go test ./...` | Pass |
| `npm run typecheck` | Pass |
| Docker runtime checks | Not possible on this machine (Docker CLI unavailable) |

The passing suite still does **not** cover every operational path:

- There is no PostgreSQL integration test.
- The Docker image/Compose stack is not built or started in CI.
- There is no live multi-node deployment test.
- SSH host-key negotiation, remote metrics, and the helper-image Compose path
  are unit-tested where possible but not exercised against a real remote host.

---

## Severity summary

| ID | Severity | Finding |
|---|---|---|
| C1 | Critical | PostgreSQL support is completely broken |
| C2 | Critical | No admin authorization; any member can escalate to admin |
| C3 | Critical | Arbitrary host-path deletion and host-path mounting |
| C4 | Critical | `baseDirectory` path traversal can delete `/data` or copy `/` |
| H1 | High | Documented Docker Compose path cannot run as written |
| H2 | High | Enabling 2FA locks the user out of the web UI |
| H3 | High | Remote nodes are not used for workloads |
| H4 | High | Docker/Traefik names collide across applications |
| H5 | High | Provisioned databases are unreachable |
| H6 | High | Service deployments always fail |
| H7 | High | TOTP secrets are stored in plaintext |
| M1 | Medium | Renaming an application leaves the old container running |
| M2 | Medium | Application updates do not sync Traefik routes |
| M3 | Medium | Toggling auto-deploy silently wipes runtime settings |
| M4 | Medium | Deleting projects/environments/services leaks infrastructure |
| M5 | Medium | SSH host keys are not verified |
| M6 | Medium | WebSocket origin checking is disabled |
| M7 | Medium | No login rate limiting |
| M8 | Medium | `.env.example` ships a real, fixed `AUTH_SECRET` |
| M9 | Medium | Backups are a stub, not a backup |
| M10 | Medium | Deployment logs grow without bound |
| M11 | Medium | Slow SSE consumers are not actually disconnected |
| L1–L14 | Low | Smaller correctness, UX, and security issues |

---

# Critical findings

## C1. PostgreSQL support is completely broken

`DB_DRIVER=postgres` cannot start for several independent reasons.

### C1.1 Wrong database driver name

`internal/config/config.go:28` returns the driver name `"postgres"`:

```go
func (d Dialect) String() string { return string(d) }
```

`internal/db/db.go:95` then calls:

```go
handle, err := sql.Open(dialect.String(), dsn)
```

But the imported pgx stdlib driver registers itself as **`"pgx"`** (`internal/db/driver.go:11`). The result is:

```text
sql: unknown driver "postgres" (forgotten import?)
```

This alone prevents the process from starting with Postgres.

### C1.2 SQLite-style placeholders everywhere

Every repository uses `?` placeholders, but pgx requires `$1`, `$2`, etc. There is no `Rebind` or other translation layer anywhere in the repository.

Examples:

- `internal/db/users.go:20-23`
- `internal/db/deployments.go:16-21`
- `internal/db/jobs.go:30-34`
- `internal/db/db.go:157` — the `schema_migrations` insert also uses `?`

### C1.3 Empty strings inserted into nullable `TIMESTAMPTZ` columns

`internal/db/helpers.go:31-36`:

```go
func TextOptional(t *time.Time) string {
	if t == nil {
		return ""
	}
	return Text(*t)
}
```

The comment says this "renders a nil-able timestamp as SQL NULL", but it returns an empty string. That value is inserted into nullable `TIMESTAMPTZ` columns:

- `internal/db/deployments.go:21` — `started_at` and `finished_at`
- `internal/db/jobs.go:31-34` — `locked_at`

Postgres rejects this with:

```text
invalid input syntax for type timestamp with time zone: ""
```

Even after fixing the driver name and placeholders, every deployment/job insert would fail.

### C1.4 Multi-statement migrations

`internal/db/db.go:152` executes an entire migration file with one `tx.Exec`. The migration files contain multiple statements. pgx's extended protocol can reject multi-statement Exec calls unless simple protocol is explicitly selected.

### C1.5 No Postgres coverage

- The README advertises Postgres at `README.md:69-71` and `README.md:258`.
- `internal/db/store_test.go` only opens SQLite (`openStore`).
- CI only runs `go test ./...`; there is no Postgres service.

**Fix direction:** use the `pgx` driver name (or register `postgres` explicitly), rebind placeholders or use `$n` in Postgres-specific queries, pass `nil` for absent timestamps, run migrations in simple protocol or split statements, and add a Postgres integration test.

---

## C2. No admin authorization; any member can escalate to admin

`internal/auth/middleware.go:70-93` defines:

- `RequireAdmin`
- `Guarded(router, requireAdmin)`

Neither is used anywhere in the codebase. `internal/api/server.go:109-112` applies only:

```go
r.Use(s.authMW.Authenticate)
r.Use(s.authMW.RequireAuth)
```

Every authenticated user therefore has access to privileged endpoints.

### Escalation path

A `member` can send:

```http
POST /api/users
Content-Type: application/json

{"email":"attacker@example.com","password":"long-password","name":"Attacker","role":"admin"}
```

`internal/api/handlers_auth.go:338-357` accepts the role and calls `RegisterAsAdmin`. The new account is an admin.

A member can also promote an existing account, including their own:

```http
PUT /api/users/{id}
{"name":"...","role":"admin"}
```

`handlers_auth.go:359-376` updates the role with no caller check.

### Other privileged actions available to every member

- Delete other users (`handlers_auth.go:378-391`)
- List all users and email addresses (`handlers_auth.go:319-329`)
- Create/update/delete nodes and SSH keys (`server.go:152-169`, `handlers_nodes.go`)
- Open container or root-node terminals (`handlers_terminal.go`)
- Modify GitHub App credentials and installation links (`server.go:244-261`)
- Run global Docker cleanup/prune (`server.go:226-229`, `handlers_docker.go`)
- Create databases with arbitrary images/versions and manage their containers
- Deploy arbitrary Compose stacks, including privileged containers and host mounts

The Team page currently says "Invite people from Settings once roles exist" (`web/src/routes/team.tsx:18`), but the backend already exposes role mutations and privileged endpoints. If RBAC is intentionally postponed, at minimum these endpoints need a temporary admin guard.

**Fix direction:** apply `RequireAdmin` to the admin route groups, explicitly prevent non-admins from changing roles, and add a last-admin guard.

---

## C3. Arbitrary host-path deletion and host-path mounting

### C3.1 Arbitrary deletion on application delete

`POST /api/applications/{id}/volumes` accepts a user-supplied `hostPath` without validation (`internal/api/handlers_applications.go:861-884`):

```go
volume := &db.Volume{
    // ...
    HostPath: req.HostPath,
}
```

Deleting the application with `?volumes=1` calls `os.RemoveAll` on that path (`handlers_applications.go:335-345`).

The protection function only rejects exact top-level directories (`handlers_applications.go:395-405`):

```go
protected := map[string]bool{
    "/": true, "/bin": true, "/boot": true, "/dev": true, "/etc": true, "/home": true,
    "/lib": true, "/opt": true, "/proc": true, "/root": true, "/run": true, "/sbin": true,
    "/srv": true, "/sys": true, "/tmp": true, "/usr": true, "/var": true,
}
return !protected[path]
```

These paths pass the check:

- `/etc/nginx`
- `/data`
- `/opt/meghify`
- `/var/lib/docker`
- `/home/user/.ssh`
- relative paths such as `etc`

The installer's systemd unit has **no `User=` directive** (`scripts/install.sh:778-793`), so the panel runs as root. A member can create an application, add such a volume, and delete the application to wipe arbitrary directories.

### C3.2 Arbitrary host-path mounting

The same unvalidated path is passed to Docker as a bind mount (`internal/deploy/dockerfile.go:1049-1053`):

```go
out = append(out, docker.Mount{
    Source:   v.HostPath,
    Target:   v.MountPath,
    ReadOnly: v.ReadOnly,
})
```

A member can mount `/` or `/var/run/docker.sock` into an application container. That provides host file read/write or full Docker control, depending on the image and mount options.

### C3.3 Remote-node path deletion

`os.RemoveAll(volume.HostPath)` always runs on the panel host, even when the application is assigned to a remote node. The path refers to the remote filesystem, so the panel either deletes nothing or deletes an unrelated local path with the same name.

**Fix direction:** derive managed volume paths from a server-controlled root under `DATA_DIR`, never accept a host path from the client, and restrict deletion to that root. If arbitrary bind mounts are a required feature, make them admin-only and validate the path against an allowlist.

---

## C4. `baseDirectory` path traversal can delete `/data` or copy `/`

`applicationRequest.validate` checks name, build pack, port, CPU, memory, and port mappings, but not `baseDirectory` (`internal/api/handlers_applications.go:47-74`).

The host build fallback builds a path from it (`internal/deploy/dockerfile.go:827-839`):

```go
root := filepath.Join(p.dataDir, "workspaces", app.ID)
workdir := filepath.Join(root, "source")

baseDir := strings.TrimSpace(app.BaseDirectory)
if baseDir == "" || baseDir == "/" {
    baseDir = ""
} else {
    baseDir = strings.Trim(baseDir, "/")
}
if baseDir != "" {
    workdir = filepath.Join(workdir, baseDir)
}
```

`strings.Trim` only removes slashes; it does not remove `..`.

If the directory already exists, the code goes to the fetch path (`dockerfile.go:855-862`):

```go
if err := p.gitFetch(ctx, sink, app, workdir); err != nil {
    sink.Write([]byte("[!] fetch failed, recloning\n"))
    os.RemoveAll(workdir)
    // ...
}
```

With `baseDirectory=../../..`, `workdir` becomes `/data`; with more `..` it becomes `/`. A failed `git fetch` then removes that directory. If the repository is empty, the code skips the fetch and reaches `copyDir` (`dockerfile.go:866-870`):

```go
staging := filepath.Join(root, "build")
os.RemoveAll(staging)
if err := copyDir(workdir, staging); err != nil {
    return "", fmt.Errorf("stage build context: %w", err)
}
```

That can copy `/` or `/data` into the build context.

This path is reachable when the helper image is unavailable, because `runDockerfile` falls back to the host build (`dockerfile.go:39-44`). The helper path itself also concatenates `baseDirectory` at `dockerfile.go:960-966`, allowing a `cd` outside the repository inside the helper.

**Fix direction:** reject absolute paths and any path containing `..`; clean the path and verify it is still under the application's workspace with `filepath.Rel`/prefix checks; never call `RemoveAll` on a path outside the managed root.

---

# High findings

## H1. The documented Docker Compose path cannot run as written

### H1.1 `/data` is not writable by the nonroot image user

The runtime image is `gcr.io/distroless/static-debian12:nonroot` (`Dockerfile:41`). The Dockerfile declares:

```dockerfile
VOLUME ["/data"]
```

(`Dockerfile:51`), but never creates or chowns `/data`.

`docker-compose.yml:20` mounts a named volume:

```yaml
- meghify-data:/data
```

A new named volume is normally root-owned. The container runs as uid/gid 65532 (`nonroot`), so it cannot create:

- `/data/meghify.db`
- `/data/workspaces`
- `/data/deployments`

The panel fails at SQLite open time or on the first deployment.

### H1.2 The Docker socket is not accessible to the nonroot user

`docker-compose.yml:19` mounts:

```yaml
- /var/run/docker.sock:/var/run/docker.sock:ro
```

On a standard Linux host the socket is `root:docker` with mode `0660`. The container user is not in the host `docker` group, and Compose does not add one via `group_add`. The panel therefore gets `EACCES` when it tries to talk to the engine.

The `:ro` suffix does **not** make the Docker API read-only. It only prevents modifying the socket file itself; HTTP API requests still work normally.

The installer's systemd path runs as root, which masks both problems, but:

- the README quick start uses `docker compose up -d` (`README.md:113-124`);
- the installer's fallback when no release exists uses Compose (`scripts/install.sh:679,734,769-775`).

**Fix direction:** add `RUN mkdir -p /data && chown 65532:65532 /data` to the runtime stage, pass the host Docker GID with `group_add`, and add a CI smoke test that starts the Compose stack and polls `/api/health`.

---

## H2. Enabling 2FA locks the user out of the web UI

The backend returns HTTP 401 with the message `"totp code required"` when a password is correct but the second factor is missing (`internal/api/server.go:355-356`; `internal/api/handlers_auth.go:71-77`).

The frontend intercepts **every** 401 before reading the response body (`web/src/lib/api.ts:44-48`):

```ts
if (response.status === 401) {
  window.location.assign("/login");
  throw new ApiError(401, "Not authenticated");
}
```

`login.tsx:46-53` only reveals the authenticator-code input if the error message contains `"totp"`:

```ts
if (message.toLowerCase().includes("totp")) {
  setNeedsCode(true);
  setError("Enter the code from your authenticator app.");
  return;
}
```

That message never reaches this code because `request()` replaced it with `"Not authenticated"` and started a navigation. The result:

- The first login attempt with a 2FA-enabled account reloads `/login`.
- The code field never appears.
- The user cannot complete login through the web UI.

Wrong passwords have the same problem: instead of showing “invalid credentials,” the page reloads.

Additionally, Settings only displays the 2FA status (`web/src/routes/settings.tsx:56-59`); the `useTotpEnroll`, `useTotpConfirm`, and `useTotpDisable` hooks are unused. There is no UI to enable or disable 2FA.

**Fix direction:** do not automatically redirect for 401 responses from `/auth/login` or `/auth/session`; parse the error body first; return a dedicated `totpRequired` flag or status code; add the 2FA controls to Settings.

---

## H3. Remote nodes are not used for workloads

The `nodeId` field exists on applications, databases, and services, but the deployment pipeline is constructed with only the local Docker client (`cmd/meghify/main.go:117-145`).

The pipeline stores that client (`internal/deploy/pipeline.go:100`) and uses it for every build, release, image transfer, proxy operation, and cleanup (`pipeline.go:421-441`).

The deploy job has an empty node branch (`cmd/meghify/main.go:243-247`):

```go
if body.ApplicationID != "" {
    if app, err := store.Applications.ApplicationByID(ctx, body.ApplicationID); err == nil && app.NodeID != "" {
        // A node-pinned application builds on that node's engine.
    }
}
```

The comment describes behavior that is not implemented.

Other operations also bypass `clientForNode`:

- Application restart: `internal/api/handlers_deploy.go:139-158`
- Application stop: same path
- Application logs: `handlers_deploy.go:188-207`
- Deployment cancel status check: `handlers_deploy.go:294`
- Application delete cleanup: `internal/api/handlers_applications.go:325-380`
- Database start/stop/recreate/delete: `internal/api/handlers_databases.go:229-233,262-320`

Route synchronization explicitly skips remote applications (`internal/deploy/dynamic.go:223-227`):

```go
if app.NodeID != "" {
    // Node deployments manage their engine's proxy through the node
    // channel; the local proxy knows nothing about them.
    return nil
}
```

No such node channel exists for proxy management, so remote applications never receive routes.

Remote metric history is also missing: `collectMetrics` only samples and writes the local node (`cmd/meghify/maintenance.go:38-87`). `handlers_nodes.nodeMetrics` reads history by node ID, so remote nodes show empty charts.

Finally, the application and database creation UI does not expose a server selector (`web/src/routes/projects.$projectId.tsx`), so the `nodeId` field is not reachable from the normal UI flow.

**Fix direction:** inject the node registry into the pipeline and resolve the correct engine client for every operation. Build, release, logs, restart, stop, delete, database lifecycle, proxy/routing, and metrics all need to use the application/database node. Add an end-to-end remote-node test.

---

## H4. Docker/Traefik names collide across applications

Application names are unique only per environment (`internal/db/migrations/001_init.sqlite.sql:110`). Database names are also unique only per environment (`001_init.sqlite.sql:138`). But Docker, network, image, and Traefik identifiers are derived from the name alone.

### Container names

`internal/docker/client.go:257-265`:

```go
func containerName(name string) string {
    return "meghify-" + sanitize(name)
}
```

### Networks

`internal/deploy/pipeline.go:482-484`:

```go
func networkName(app *db.Application) string {
    return "meghify-" + sanitizeName(app.Name)
}
```

### Traefik router/service/middleware names

`internal/deploy/dynamic.go:58-59,97-116` uses `slug := sanitizeName(app.Name)` for:

- `https-<slug>-<index>`
- `http-<slug>-<index>`
- `<slug>-<index>` services
- `<slug>-gzip`
- redirect and robots middlewares

### Compose project names

`internal/deploy/dockerfile.go:440,460` uses `sanitizeName(app.Name)` as the Compose project name.

### Collision cases

- `production/api` and `staging/api`: different database rows, same container/network/router names.
- `api` and `API` in the same environment: SQLite's default `UNIQUE` is case-sensitive, but both sanitize to `api`.
- Database names such as `db` and `DB` collide as `meghify-db`.

Effects include:

- Container creation fails because the name already exists.
- The second application attaches to the first application's network.
- Traefik router names are duplicated across files, causing undefined/overwritten routing.
- Deleting one application can attempt to remove a network shared by another.

**Fix direction:** include the application/database ID (or a short ID) in all Docker, network, Compose, and Traefik identifiers. Alternatively, enforce globally normalized, case-insensitive names.

---

## H5. Provisioned databases are unreachable

`createDatabase` never sets `PublicPort` (`internal/api/handlers_databases.go:150-158`). `CreateContainer` skips host port bindings when the host port is zero (`internal/docker/client.go:202-209`):

```go
for cPort, hPort := range spec.Ports {
    if hPort <= 0 {
        continue
    }
    // ...
}
```

The database container also has no `Networks` configured (`handlers_databases.go:180-188`), while application containers join `meghify-<app>` (`internal/deploy/dockerfile.go:505-524`). The database therefore sits on Docker's default bridge, unreachable by the application containers.

No code links a database to an application or injects a connection URL. The database handler only stores `ROOT_PASSWORD` under `TargetDatabase` (`handlers_databases.go:203-214`). The UI nevertheless says the credentials are "already attached to this database's environment variables for linked apps" (`web/src/routes/projects.$projectId.tsx:1332-1337`), but no linkage mechanism exists.

The database UI also displays "not published" for every database because `publicPort` is always zero (`web/src/routes/databases.tsx:71`).

### Missing authentication for Redis and MongoDB

`databaseEnv` (`handlers_databases.go:67-74`) sets:

```go
"POSTGRES_PASSWORD=" + rootPassword,
"MYSQL_ROOT_PASSWORD=" + rootPassword,
"MARIADB_ROOT_PASSWORD=" + rootPassword,
"MONGO_INITDB_ROOT_PASSWORD=" + rootPassword,
```

It does not set:

- `REDIS_PASSWORD` — Redis starts with no authentication.
- `MONGO_INITDB_ROOT_USERNAME` — the official Mongo image does not create an authenticated root user from the password alone.

The generated credentials returned for Redis and MongoDB therefore do not work as advertised.

**Fix direction:** pick a connectivity model (publish a port or attach the database to the application network), allocate the host port, inject `DATABASE_URL`/host/port into linked apps, and set the correct per-engine environment variables.

---

## H6. Service deployments always fail

`deployService` marks the service `deploying` and enqueues `JobBuildImage` (`internal/api/handlers_services.go:117-132`):

```go
payload := `{"serviceId":"` + id + `"}`
if _, err := s.store.Jobs.Enqueue(r.Context(), db.JobBuildImage, payload, time.Now(), 2); err != nil {
    // ...
}
```

The job handler calls:

```go
return pipeline.Run(ctx, deploy.Payload{
    DeploymentID:  service.ID,
    ApplicationID: "",
    NoCache:       false,
})
```

(`cmd/meghify/main.go:251-267`)

`Pipeline.Run` immediately loads a deployment:

```go
deployment, err := p.store.Deployments.DeploymentByID(ctx, payload.DeploymentID)
```

(`internal/deploy/pipeline.go:266`)

A service ID is not a deployment ID, so this always returns `db.ErrNotFound`. The job fails, and the service status is never reconciled from `deploying`.

Deleting a service also does not run `docker compose down` or remove its containers (`handlers_services.go:108-115`).

**Fix direction:** either remove the service feature until it has its own deployment model, or create a service deployment path that applies and tracks the Compose stack.

---

## H7. TOTP secrets are stored in plaintext

`EnrollTOTP` writes the raw base32 seed through `SetTOTP` (`internal/auth/totp.go:30`):

```go
if err := s.store.Users.SetTOTP(ctx, user.ID, key.Secret(), false); err != nil {
    return "", "", err
}
```

`SetTOTP` stores the value directly (`internal/db/users.go:107-110`):

```go
`UPDATE users SET totp_secret = ?, totp_confirmed = ?, totp_enabled = ?, updated_at = ? WHERE id = ?`
```

Unlike SSH private keys and environment variables, the TOTP seed is never passed through `SecretBox`.

The README claims:

> Stored secrets, SSH private keys, and TOTP seeds are sealed with AES-256-GCM under `AUTH_SECRET`.

(`README.md:299-300`)

That is false for TOTP. A database leak exposes every user's second factor.

**Fix direction:** seal the TOTP secret on write and open it on read, using the existing `SecretBox`. Add a migration for existing plaintext seeds.

---

# Medium findings

## M1. Renaming an application leaves the old container running

`updateApplication` changes the database row (`internal/api/handlers_applications.go:242-298`) but does not:

- rename the existing container;
- rename the network;
- stop/remove the old container;
- resync routes.

On the next deployment, `ContainerByName(newName)` finds nothing, so a new container is created. The old `meghify-<old-name>` container keeps running, consuming resources and potentially serving traffic directly.

**Fix direction:** either disallow renaming applications after creation, or implement a rename operation that renames the container/network and updates routes atomically.

---

## M2. Application updates do not sync Traefik routes

`updateApplication` does not call `syncRoutes`. It can change:

- `name` (Traefik backend name);
- `port` (Traefik backend port);
- `redirectHttps` (domain middleware).

The handler updates domain rows for `redirectHttps` (`handlers_applications.go:275-285`) but never pushes the new configuration to Traefik. Route sync only happens on domain create/update/delete (`handlers_applications.go:518,566,649`).

As a result, toggling "Redirect HTTP to HTTPS" in application settings can appear to save but have no effect until a later domain edit, deployment, or restart reconciliation.

**Fix direction:** call `syncRoutes` after any update that changes name, port, or redirect settings.

---

## M3. Toggling auto-deploy silently wipes runtime settings

`GitHubCard` sends a partial `PUT /api/applications/{id}` body (`web/src/routes/applications.$applicationId.tsx:417-429`):

```ts
api.put<Application>(`/applications/${applicationId}`, {
  name: app.name,
  description: app.description,
  gitRepository: app.gitRepository,
  gitBranch: app.gitBranch,
  buildPack: app.buildPack,
  dockerfile: app.dockerfile,
  baseDirectory: app.baseDirectory,
  port: app.port,
  healthCheck: app.healthCheck,
  nodeId: app.nodeId,
  autoDeploy: enabled,
});
```

It omits:

- `portMappings`
- `cpuLimit`
- `memoryLimit`
- `preDeployCommand`
- `postDeployCommand`

The update handler assigns those fields unconditionally (`handlers_applications.go:260-290`), so omitted values become zero/empty. Toggling auto-deploy erases port mappings, CPU/memory limits, and lifecycle hooks.

**Fix direction:** make the endpoint a true PATCH with pointer fields, or always send the complete payload from every card.

---

## M4. Deleting projects/environments/services leaks infrastructure

`deleteProject` only deletes the project row (`internal/api/handlers_projects.go:86-94`). `deleteEnvironment` only deletes the environment row (`126-132`). Foreign keys cascade the database records, but no cleanup runs for:

- running application containers;
- Docker networks;
- named volumes;
- Traefik route files;
- deployment log files;
- env-var and volume rows (polymorphic targets are not foreign-keyed).

Once the application rows are gone, the panel cannot reconcile the orphaned containers. The same problem exists for service deletion (`handlers_services.go:108-115`) and, for remote nodes, database deletion.

**Fix direction:** enumerate child applications/databases before deleting a project or environment, run the same teardown used by `deleteApplication`, and remove tracked files and proxy configuration.

---

## M5. SSH host keys are not verified

`internal/node/ssh.go:147-153` uses:

```go
HostKeyCallback: ssh.InsecureIgnoreHostKey()
```

with the comment:

```go
// Nodes are provisioned by us, so host keys are not pinned in v1.
```

A network attacker can impersonate a remote node and intercept the Docker tunnel, install script output, and remote terminal traffic. The README acknowledges this gap at `README.md:306`.

**Fix direction:** implement trust-on-first-use. Store the host key fingerprint on the node record and verify it on every connection.

---

## M6. WebSocket origin checking is disabled

Both terminal handlers use:

```go
conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
    InsecureSkipVerify: true, // browser requests are same-origin
})
```

- `internal/api/handlers_terminal.go:41-43` — application terminal
- `internal/api/handlers_terminal.go:81-83` — node terminal

The comment is incorrect: `Origin` is attacker-controlled and is not validated.

`SameSite=Lax` blocks most cross-site WebSocket handshakes, but a same-site attacker (for example, a compromised subdomain) can still open an interactive container or root-node terminal using the user's cookie.

**Fix direction:** validate `Origin` against `PANEL_PUBLIC_URL` or the request host before accepting the upgrade.

---

## M7. No login rate limiting

There is no throttle or lockout on `POST /api/auth/login` (`internal/api/handlers_auth.go:64-80`). Each attempt performs Argon2id hashing at 64 MB, so concurrent attempts are both:

- a password brute-force vector;
- a memory/CPU exhaustion vector.

The unknown-email path also intentionally spends the same hashing time (`internal/auth/service.go:143-145`), which makes the DoS cost symmetric.

**Fix direction:** add per-IP and per-account rate limits with exponential backoff, and consider a small global concurrency cap for password verification.

---

## M8. `.env.example` ships a real, fixed `AUTH_SECRET`

`.env.example:7` contains:

```text
AUTH_SECRET=ed1d6287a5613d19daac27373551b81ed945b8ef98503f7083ee45d0ca3ec20f
```

The README tells users to copy this file (`README.md:117`), and the release archive includes it (`scripts/install.sh:247-248`, `.github/workflows/release.yml:70`). Anyone who uses the example without overriding it encrypts SSH keys, Git credentials, environment variables, and any other sealed data with a key published in the repository.

The quick start appends a second `AUTH_SECRET` line (`README.md:120`), which overrides the example in Docker Compose's last-wins parser, but it leaves a duplicate key and an insecure example for every other workflow.

**Fix direction:** replace the value with an empty placeholder and state that the panel refuses to start without a generated secret (or generates one and persists it safely).

---

## M9. Backups are a stub, not a backup

`createBackup` (`internal/api/handlers_databases.go:350-379`) creates a row, but only enqueues a job if `schedule` is non-empty:

```go
if req.Schedule != "" {
    if _, err := s.store.Jobs.Enqueue(r.Context(), db.JobBackup, payload, time.Now(), 1); err != nil {
        // ...
    }
}
```

A request without a schedule leaves the backup permanently `pending`.

`runBackup` (`cmd/meghify/maintenance.go:137-153`) does not dump or upload anything:

```go
logger.Info("backing up database", "backup", backupID, "database", database.Name, "kind", database.Kind)
return store.Resources.FinishBackup(ctx, backupID, db.StatusRunning, 0, "")
```

It marks the backup `running` with size `0` and never transitions to success. The README claims scheduled backups at `README.md:37`, and the Jobs page labels `backup` jobs as "Database backup" (`web/src/routes/jobs.tsx:14`).

**Fix direction:** remove the feature from the UI/README until implemented, or implement `dump`/`pg_dump`/`mysqldump`/`redis-cli --rdb`/`mongodump`, upload to S3 if configured, and mark success/failure with size and error.

---

## M10. Deployment logs grow without bound

Build output is written to:

```go
logPath := filepath.Join(p.dataDir, "deployments", deployment.ID+".log")
```

(`internal/deploy/pipeline.go:285`)

There is:

- no per-log size cap;
- no retention policy;
- no cleanup in `clearDeployments` (`handlers_deploy.go:314-320` deletes DB rows only);
- no cleanup when an application or project is deleted.

A long-running install accumulates one log file per deployment forever. Large build output can also fill the data volume.

**Fix direction:** cap log size while streaming, add a retention policy, and delete log files when deployment history or the parent application is removed.

---

## M11. Slow SSE consumers are not actually disconnected

In `internal/realtime/hub.go`:

```go
func (c *client) send(e Event) bool {
    c.mu.Lock()
    defer c.mu.Unlock()
    if c.closed {
        return false
    }
    select {
    case c.ch <- e:
        return true
    default:
        c.closed = true
        return false
    }
}
```

(`hub.go:45-59`)

When the 64-event buffer is full, `send` marks the client closed and returns `false`. `Broadcast` then calls `remove` (`hub.go:102-106`), which calls `c.close()`:

```go
func (c *client) close() {
    c.mu.Lock()
    defer c.mu.Unlock()
    if c.closed {
        return
    }
    c.closed = true
    close(c.ch)
}
```

(`hub.go:61-69`)

Because `send` already set `closed = true`, `close` returns without closing the channel. The `Stream` goroutine remains blocked in its select loop and sends keepalives every 15 seconds indefinitely. The comment says "A slow consumer is dropped," but the connection and goroutine leak until the client disconnects.

**Fix direction:** close the channel in the failure path or cancel the stream context when the buffer overflows.

---

# Low-severity findings

## L1. `CreateUser` writes `TOTPEnabled` into `totp_confirmed`

`internal/db/users.go:20-24`:

```go
q := `INSERT INTO users (..., totp_enabled, totp_confirmed, ...)
      VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
if _, err := r.db.ExecContext(ctx, q,
    u.ID, u.Email, u.PasswordHash, u.Name, u.Role, u.TOTPSecret, u.TOTPEnabled, u.TOTPEnabled, now, now,
); err != nil {
```

The seventh argument is written to `totp_enabled` and the eighth to `totp_confirmed`. Both use `u.TOTPEnabled`; the `totp_confirmed` value should come from its own field (or be false for a new user).

Currently harmless because new users are always created with `TOTPEnabled == false`, but it is a latent correctness bug.

## L2. API token ownership and scopes

- `DELETE /api/tokens/{id}` deletes by ID without checking that the token belongs to the caller (`internal/api/handlers_auth.go:425-432`). IDs are UUIDs, but this is still missing object-level authorization.
- `APIToken.Scopes` is serialized and deserialized (`internal/db/helpers.go:38-60`), but no authorization code ever reads it. Every token has the full permissions of its owner.

## L3. Password change logs out the current session

`changePassword` calls `RotateSessions`, which deletes every session for the user (`internal/api/handlers_auth.go:197-203`, `internal/auth/service.go:268-272`). The current session is included, but the response and toast say only "Other sessions were signed out" (`web/src/routes/settings.tsx:28`). The next request 401-redirects to login.

## L4. Job queue crash recovery and requeue duplication

`Claim` only reclaims stale `retrying` jobs (`internal/db/jobs.go:86-103`); a process crash leaves `running` jobs stuck forever. `Requeue` updates any job to pending without checking its current status (`internal/db/jobs.go:143-149`), so retrying a running job can execute it twice.

## L5. Frontend retry predicate never matches `ApiError(401)`

`web/src/lib/api.ts:13-15`:

```ts
retry: (failureCount, error) => {
  if (error instanceof Error && error.message.includes("401")) return false;
  return failureCount < 2;
},
```

`ApiError(401, "Not authenticated")` has a message of `"Not authenticated"`, not `"401"`. The predicate never suppresses 401 retries; it should check `error instanceof ApiError && error.status === 401`.

## L6. Token creation can silently lose the token

`web/src/routes/settings.tsx:140-143`:

```ts
const result = await api.post<{ token: string }>("/tokens", { name: "pipeline" });
await navigator.clipboard.writeText(result.token);
toast.success("Token created and copied to clipboard");
```

`navigator.clipboard` is unavailable on plain-HTTP origins and may be denied by browser permission. The token is created on the server, but the user never sees it and no error is shown.

## L7. GitHub Enterprise URL display

`web/src/routes/projects.$projectId.tsx:640`:

```tsx
`${gitHubWebBase(app).replace("https://", "")}/apps/${app.slug}`
```

This only strips `https://`. An `http://` GHE base displays as `http://...`, and base URLs containing a path are not handled consistently. The same pattern appears in `sources.$appId.tsx:134` and `sources.index.tsx:171`.

## L8. `errNoEngine` is `context.Canceled`

`internal/api/helpers.go:40`:

```go
var errNoEngine = context.Canceled
```

`clientForNode` returns this when no Docker engine is configured. Handlers then respond 503 with `"context canceled"`, which is misleading and can be misclassified by `errors.Is(err, context.Canceled)` callers.

## L9. Configuration test comment contradicts its assertion

`internal/config/config_test.go:14-20` says:

> A pinned `BUILD_HELPER_IMAGE` wins exactly as given: cleanup derives the protected reference from it, so no trimming may alter it.

But `EffectiveHelperImage` trims the value and the test expects the trimmed result (`"ghcr.io/tamalmaity-dev/meghify-helper:test"`). The comment should be corrected, or the behavior should be changed.

## L10. Audit IP addresses are spoofable

`clientIP` trusts `X-Forwarded-For` and `X-Real-IP` unconditionally (`internal/api/server.go:391-401`), and the router also uses `middleware.RealIP`. Any client can choose the IP recorded in audit logs. This will also defeat IP-based rate limiting unless the proxy trust boundary is defined.

## L11. Raw names are used as container hostnames

`internal/docker/client.go:213`:

```go
Hostname: spec.Name,
```

`spec.Name` can contain spaces and other characters because application/database name validation only checks for non-empty (`handlers_applications.go:47-74`, `handlers_databases.go:134-137`). Container names are sanitized, but hostnames are not. Names with spaces or special characters can fail Docker validation; punctuation-only names can break the final rename lookup.

## L12. Default health check assumes `curl`

`healthCheckFor` (`internal/deploy/dockerfile.go:1069-1080`) runs:

```go
Test: []string{"CMD-SHELL", "curl -fsS http://127.0.0.1:" + fmt.Sprint(app.Port) + app.HealthCheck + " || exit 1"},
```

The UI defaults the health-check path to `/` (`web/src/routes/projects.$projectId.tsx:194`). Images without `curl` will be marked unhealthy even when the application is working. A health check should use a binary guaranteed by the image, or be disabled by default.

## L13. SSE replay is global

Any authenticated user can open `/api/stream` without a channel and receive the last 200 events from all deployments (`internal/realtime/hub.go:139-168`). With the current single-role model every user already sees everything, but this becomes a data leak once real RBAC or multi-tenancy is added.

## L14. Docker Compose `:ro` socket is a false security control

`docker-compose.yml:19` mounts the Docker socket as read-only. This does **not** restrict Docker API operations; it only prevents changes to the socket file. If the intent is to limit Docker access, the panel needs a different mechanism entirely (and the panel currently requires write access to manage containers).

---

# What is done well

- The Go package layout is clean and consistently documented.
- SQL values are parameterized throughout; I did not find SQL injection through user input.
- GitHub webhook signature verification is correct and uses `hmac.Equal`.
- Session tokens and API tokens are hashed at rest.
- Most secrets (SSH keys, environment variables, GitHub App credentials) are sealed with AES-GCM.
- The job claim uses an atomic guarded `UPDATE`.
- The deployment pipeline has sensible cancellation, timeout, log tee, and recovery structure.
- Graceful shutdown, embedded SPA serving, and SSE framing are handled thoughtfully.
- The frontend typecheck and production build are clean.
- The CI workflow runs `go vet`, `go test`, frontend typecheck, and frontend build.

---

# Recommended fix order

1. **Postgres:** fix the driver name, placeholders, nullable timestamps, and migrations; add an integration test. If it is not going to be supported, remove the claim.
2. **Authorization:** apply `RequireAdmin` to privileged route groups; block self-role escalation; guard user creation, nodes, terminals, Docker cleanup, and GitHub credentials.
3. **Host-path safety:** remove client-supplied host paths from volumes; constrain `baseDirectory`; reject `..`, absolute paths, and symlink escapes.
4. **Docker packaging:** create/chown `/data`, pass the Docker socket group, and add a `docker compose up` smoke test.
5. **2FA:** fix the 401 handling and expose enrollment/disable controls in Settings.
6. **Feature truthfulness:** either complete or clearly remove/disable remote nodes, database connectivity, service deployments, and backups.
7. **Identifiers and cleanup:** include IDs in Docker/Traefik names, implement rename/route sync, and add teardown for project/environment/service deletion.
8. **Security hardening:** SSH host-key pinning, WebSocket Origin checks, login rate limiting, TOTP encryption, and a generated `AUTH_SECRET`.
9. **Test coverage:** add auth, Postgres, deployment, Docker harness, and multi-node tests.

---

# Test coverage gaps

The current suite gives a false sense of safety for the issues above:

- `internal/db/store_test.go` exercises SQLite only.
- `internal/auth` has no tests for login, TOTP, sessions, cookies, or authorization.
- `internal/jobs` has no tests for crash recovery or duplicate execution.
- `internal/node` has no tests for SSH host-key handling.
- `internal/realtime` has no tests for slow consumers or replay isolation.
- `internal/api` tests cover GitHub helpers but not route authorization or the TOTP login flow.
- No test starts the Docker image, the Compose stack, or a remote-node tunnel.
- No test deploys a service, a database, or an application with a non-default node.
