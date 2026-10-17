# Meghify

A lightweight, self-hostable PaaS. Deploy applications from Git, provision
databases, and route domains on your own hardware.

The open-source alternative to Vercel, Netlify, and Heroku, built for VPS,
bare-metal servers, and Raspberry Pi.

**The point of difference is cost to your machine.** A comparable panel
Coolify or Dokploy idles at 350 MB-1.2 GB of RAM across several processes and
containers. Meghify holds about 16 MB working set as a single static binary
with the database inside the same process, and uses effectively no CPU when
idle. On a 2 GB VPS that difference is most of the machine.

```
                            idle RAM        idle CPU        processes
Coolify (PHP stack)         ~750-1200 MB     5-7%            7+
Dokploy (Node + Next.js)    ~350-450 MB      <2%             4+
Meghify (Go + SQLite)       ~16 MB           ~0%             1
```

---

## What it does

- **Deploy applications** from GitHub, GitLab, Bitbucket, or Gitea with a
  Dockerfile, Docker Compose, or an auto-detecting build pack.
- **Connect GitHub with a GitHub App** created from the panel — private
  repositories, a repository picker, and redeploys on every push.
- **Provision databases** (PostgreSQL, MySQL, MariaDB, Redis, MongoDB) with a
  generated root password that is returned once and stored encrypted.
- **Route domains** with automatic HTTPS through Traefik.
- **Manage several servers** from one dashboard over SSH.
- **Stream build and runtime logs** live.
- **Open a shell** inside any running container.
- **Automate work** with a durable job queue that needs no Redis.
- **Back up** databases on a schedule.

---

## Architecture

```
                       ┌──────────────────────────────────┐
                       │  one static Go binary            │
                       │                                  │
   browser ──HTTP────▶ │  chi router                      │
     │                 │   ├─ /api/*   REST + SSE         │
     │                 │   ├─ /stream  live deploy output │
     │                 │   └─ /*       embedded React SPA │
     │                 │                                  │
     └────WebSocket──▶ │  container terminal (xterm.js)   │
                       │                                  │
                       │  auth  ·  jobs  ·  deploy        │
                       │  realtime  ·  proxy  ·  node     │
                       │                                  │
                       │  SQLite (WAL)  ·  Docker Engine  │
                       └──────────────────────────────────┘
```

### Why these choices

**Go for the backend.** A single static binary with no CGO. The panel is a
control plane: a handful of admin users, low write throughput, reads dominating.
That is close to the worst case for an interpreted runtime and the best case for
a compiled one. The React SPA is compiled into the binary with `embed`, so
production is one file and one process.

**SQLite by default, Postgres optional.** The control plane does not need a
networked database. SQLite in WAL mode inside the panel process costs no extra
memory and no extra container. `DB_DRIVER=postgres` switches to the same schema
for anyone who wants a networked store later.

**No Redis.** The job queue is a table. Workers claim rows with a guarded
`UPDATE`, so several panel replicas could share the queue without a lock
service. Cron schedules fire by enqueuing, which means a restart never loses a
schedule.

**SSE for logs, WebSocket only for the terminal.** Deployment output and status
flow one way, so a server-sent event is cheaper to hold open than a WebSocket
hub. The only bidirectional socket is the container shell, where the browser has
to send keystrokes.

**Traefik for the proxy.** It reads routing rules from container labels, so the
panel only sets labels when it creates a container and TLS then happens with no
proxy API calls.

---

## Tech stack

| Layer | Choice |
|---|---|
| Backend | Go 1.26, `net/http` + chi |
| Database | SQLite (WAL) via `modernc.org/sqlite`, optional Postgres via `pgx` |
| Queries | `database/sql` with typed repositories |
| Migrations | Plain SQL files, applied at boot, one per dialect |
| Auth | Argon2id, opaque session tokens, TOTP 2FA, `megh_`-prefixed API tokens |
| Docker | Official Engine API client, BuildKit builds over tar contexts |
| Remote nodes | `golang.org/x/crypto/ssh` + a local TLS tunnel to the remote engine |
| Queue | Database table + `robfig/cron/v3` |
| Frontend | React 19 + TypeScript + Vite 7 |
| UI | Tailwind CSS v4, radix primitives, lucide icons, Recharts, xterm.js |
| Real-time | Server-Sent Events + TanStack Query |

---

## Quick start

Requirements: Docker, and a machine with at least 512 MB of RAM. There is no
separate database or cache to install.

```bash
# 1. Clone and configure
git clone https://github.com/tamalmaity-dev/meghify.git
cd meghify
cp .env.example .env

# 2. Set the secret that encrypts credentials at rest
echo "AUTH_SECRET=$(openssl rand -hex 32)" >> .env

# 3. Run
docker compose up -d
```

Then open `http://your-server:8080` and create the first admin account. That
account is the owner; self-registration stays closed unless you set
`ALLOW_SIGNUP=true`.

### One-line install

On a fresh VPS (Ubuntu, Debian, Fedora, Rocky/Alma, Alpine), paste one command
as root:

```bash
curl -fsSL https://raw.githubusercontent.com/tamalmaity-dev/meghify/main/scripts/install.sh | bash
```

The installer mirrors the Coolify/Dokploy flow: it **checks the machine
first**, then installs, then prints a clickable link to the panel.

Preflight checks (reported individually, install aborts before touching
anything if one blocks):

- Linux kernel + distribution (Ubuntu, Debian, Fedora, RHEL/Rocky/Alma, Alpine,
  Arch via the Docker install path) and architecture (amd64, arm64, armv7)
- root access, and a download tool (curl or wget — no other host packages)
- memory: 512 MB floor, 700 MB+ comfortable; disk: 2 GB free
- the chosen port is not already taken
- Docker engine + compose plugin — **installed automatically when missing**
  (get.docker.com). Docker powers app deploys; the panel itself is a single
  compiled binary, so no toolchain is needed on the host.

Then it downloads the latest **release binary** from GitHub (built by the
release workflow), installs it as a `meghify` systemd service, generates
`AUTH_SECRET` into `/opt/meghify/.env`, polls `/api/health` until the API
answers, and prints the final link, e.g. `http://203.0.113.4:8080`. Click it
to create the first admin account. When no release binary exists yet, the
installer falls back to compiling the source snapshot inside Docker (this
takes a few minutes on the first run).

Commands and flags (also passable through the pipe with `bash -s --`):

```bash
install.sh          install the latest release (default; `install` works too)
  --uninstall       remove the panel, its data and the meghify CLI
  --update          fetch the newest release and restart the panel
  --version         print the installer, binary and running versions
  --from-source     ignore releases: build with a host toolchain
                    (git, Go 1.26+, Node 20+) instead
  --yes             answer prompts automatically (uninstall in pipes)
  --verbose         stream every command instead of hiding its output

# options through the pipe
curl -fsSL .../install.sh | PANEL_DIR=/srv/meghify APP_PORT=8443 bash
```

Afterwards the helper command manages the install:

```bash
meghify status      service state (journalctl when running under systemd)
meghify logs        follow the panel logs
meghify restart     restart the panel
meghify version     installed binary vs running API vs latest release
meghify update --check   print whether a newer release exists
meghify update      upgrade to the latest release (binary swap with
                    automatic rollback when the health check fails; Docker
                    installs are rebuilt from source instead)
meghify uninstall --yes  remove everything
```

Re-running the installer is safe: the existing `.env` (and its `AUTH_SECRET`,
and the port it carries) is preserved.

### Releases

Pushing a tag like `v0.1.0` triggers the release workflow: it builds the
frontend, cross-compiles linux amd64/arm64/armv7 binaries with the version
baked in, packages them with `docker-compose.yml` + `.env.example`, stamps
`install.sh` with the commit, and uploads everything as release assets. You
can also grab the pinned installer from a release:

```bash
curl -fsSL https://github.com/tamalmaity-dev/meghify/releases/latest/download/install.sh | bash
```

Every push to `main` runs the CI workflow (`go vet`, `go test`, web
typecheck + build).

Full details live in the docs folder:

| Doc | Contents |
|---|---|
| [docs/install.md](docs/install.md) | every preflight check, install modes, all flags, the `meghify` helper, update + rollback, uninstall, troubleshooting |
| [docs/ci-release.md](docs/ci-release.md) | the CI gates, the release workflow, how versions get stamped into binaries |
| [docs/github-apps.md](docs/github-apps.md) | connecting GitHub: creating the app from the panel, installations, repository pickers, push-to-deploy webhooks |

---

## Local development

You need Go 1.26+, Node 22+, and a Docker engine.

```bash
make web        # build the frontend into internal/webui/dist
make run        # build and run the panel with dev settings
make test       # Go tests
make vet        # go vet + frontend typecheck
make measure    # report the panel's steady-state memory and CPU
```

For a two-process workflow with hot reload, run `make dev`. The Vite server runs
on `:5173` and proxies `/api` and the SSE stream to the Go panel on `:8080`.

### Key endpoints

| Route | Purpose |
|---|---|
| `GET` `/api/health` | Liveness |
| `POST` `/api/auth/bootstrap` | Create the first admin |
| `POST` `/api/auth/login` | Sign in |
| `GET` `/api/stream?channel=deployment:<id>` | Live build output |
| `GET` `/api/applications/{id}/terminal` | Container shell (WebSocket) |
| `GET` `/openapi.json` | API schema (planned) |

---

## Configuration

Everything is an environment variable. `.env.example` documents each one with its
default. The ones that matter:

| Variable | Default | Notes |
|---|---|---|
| `AUTH_SECRET` | (required) | Encrypts secrets at rest. Losing it means re-entering every stored credential. |
| `DB_DRIVER` | `sqlite` | Or `postgres`. |
| `DB_DSN` | `$DATA_DIR/meghify.db` | Derived from `DATA_DIR` when unset. |
| `DATA_DIR` | `/data` | Holds the database, build logs, and workspaces. |
| `DOCKER_HOST` | `unix:///var/run/docker.sock` | The engine the panel talks to. |
| `WORKER_CONCURRENCY` | `4` | Concurrent deploys and backups. |
| `REGISTRY_URL` | (empty) | When set, built images are pushed. When empty, they stay on the build node. |

---

## Repository layout

```
cmd/meghify/        entrypoint, job registration, housekeeping
internal/
  api/              HTTP routes and handlers
  auth/             password hashing, encryption, sessions, middleware
  config/           environment configuration
  db/               migrations, repositories, job queue
  deploy/           the build and release pipeline
  docker/           Engine API client, BuildKit builds, tar contexts
  jobs/             durable worker pool and cron
  node/             SSH provisioning and engine tunnels
  proxy/            Traefik label generation
  realtime/         SSE hub
  webui/            embedded frontend assets
web/                React frontend (Vite)
scripts/            install, smoke test, measurement
```

---

## Security model

The panel controls the machines it runs on, so it is privileged by nature. What
that means practically:

- The container holding the panel has the Docker socket mounted, exactly as
  Coolify and Dokploy do. Anyone with an admin session has root on every server.
- Session tokens are opaque, hashed at rest in the database, and invalidated
  when a password changes.
- API tokens use a `megh_` prefix so they are recognisable in logs and config.
- Stored secrets, SSH private keys, and TOTP seeds are sealed with AES-256-GCM
  under `AUTH_SECRET`.
- Passwords use Argon2id (64 MB, t=2, p=4), which is affordable on a Pi.
- Registration is closed after the first admin exists.
- Set `PANEL_PUBLIC_URL` to an `https://` origin in production; the session
  cookie is then marked `Secure` and HSTS is sent.

Known gaps in the current version: SSH host keys are not pinned, and there is no
CSRF token (the API relies on cookie `SameSite=Lax` and JSON-only requests).

---

## Roadmap

- Git webhooks with pull-request preview deploys
- S3 backup destinations
- Node metric history and charts
- One-click service templates
- OpenAPI schema and a generated TypeScript client
- Role-based access control

---

## License

Apache 2.0. See [LICENSE](LICENSE).

## Acknowledgements

Coolify and Dokploy defined the category and shaped the feature set this project
aims at. Meghify differs mainly in the cost of running it.
