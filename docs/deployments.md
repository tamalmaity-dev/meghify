# Deployments

How Meghify turns a repository into a running container, and what the
application page shows while it happens.

---

## The pipeline

A deployment is one durable job:

1. **Prepare** — start a one-shot **build helper container** on the target
   engine and clone the repository inside it (`git clone --depth 1` of the
   tracked branch).
2. **Build** an image according to the application's build pack.
3. **Push** to the registry when one is configured (optional).
4. **Release** — a rolling swap: the replacement container is created beside
   the running one under a temporary name, started and health-checked, the old
   container is retired, and the new one is renamed into the application's
   name. There is no window without a running container, and a replacement
   that fails its health check never replaces anything. (An application that
   publishes host ports must release them first, so for those the old
   container stops just before the new one starts.)
5. **Hooks** — when configured, the pre-deployment command runs inside the
   previous container before the swap and the post-deployment command runs
   inside the new one once it is live. A failing hook fails the deployment.

Progress streams to the deployment log (durable, replayable) and to open
browsers over SSE.

## The build helper container

The panel itself is deliberately minimal: its image is distroless and carries
neither git nor nixpacks. Every git, buildpack and compose command runs inside
a disposable helper container that mounts the engine socket — the same model
Coolify uses. Two consequences:

- the host running the panel needs only Docker; and
- build tools never bloat the panel's own image or host.

The image is Meghify's own — `ghcr.io/tamalmaity-dev/meghify-helper:<version>`,
published by every release (Alpine plus git, nixpacks, railpack, and the
docker CLI with buildx and compose). `BUILD_HELPER_IMAGE` overrides it if you
want to pin your own image; it needs `git`, `nixpacks`, `railpack` and the
`docker` CLI on `PATH`. Helper containers are named `meghify-builder-…` and
are removed when the deployment ends. If the image cannot be pulled, the panel
falls back to running the tools on its own host, which keeps older installs
with those tools working.

### Keeping builds light

Builds are CPU-heavy, so at most `BUILD_CONCURRENCY` deployments (default
**1**) build at the same time — a deployment that has to wait says so in its
log. Each helper container can also be capped with `BUILD_CPU_LIMIT` (whole
cores, e.g. `1.5`) and `BUILD_MEMORY_LIMIT` (bytes or `512m` / `2g`); both
default to the engine's own limits.

## Build packs

| Pack | What runs |
|---|---|
| **Nixpacks** (default) | `nixpacks build` detects the stack and produces the image |
| **Railpack** | `railpack build` — Railway's successor to nixpacks |
| **Dockerfile** | `docker build` with the repository's Dockerfile; build args come from `build`-scope environment variables |
| **Docker Compose** | the compose definition stored on the application is applied with `docker compose up -d` |
| **Docker Image** | the reference in the repository field is pulled and released as-is |

The **Base directory** setting scopes the build to a subdirectory for every
pack.

## Automatic domains

An application created without a domain gets one:
`<name>-<id>.<server-ip>.sslip.io`, served over plain HTTP. sslip.io resolves
any subdomain that carries an IP back to that IP, so the app is reachable
immediately with no DNS work — replace it with a real domain any time from
the application's **Domains** section.

## The reverse proxy

Domains are served by a Traefik instance the panel deploys on demand: the
first deployment of an application that has domains ensures `meghify-proxy`
runs, joins the application to the shared `meghify` network, and attaches the
routing labels. Docker-compose installs ship Traefik with the panel; systemd
installs get it created here on first use.

| Variable | Default | Effect |
|---|---|---|
| `PROXY_IMAGE` | `traefik:v3.7` | the proxy image; empty disables proxy management. Needs Traefik v3.6+ (older releases cannot talk to Docker Engine 29) |
| `PROXY_HTTP_PORT` / `PROXY_HTTPS_PORT` | `80` / `443` | host ports the proxy binds — change them when another proxy already owns those ports |
| `ACME_EMAIL` | empty | registers a Let's Encrypt account; without it, HTTP routing still works and HTTPS serves Traefik's fallback certificate |

If the proxy cannot start (both ports taken by something else, for example),
the deployment still succeeds and says so in the log; the application keeps
its published ports.

## Credentials

Private repositories clone with a short-lived GitHub App installation token
minted per build (see [github-apps.md](github-apps.md)). The token never lands
in `.git/config` or the deployment log — build output is redacted — and the
helper container, with everything inside it, is destroyed when the build
finishes.

## What the application page shows

- **Deployments** — a searchable history: status, source (manual or webhook),
  commit and message, when the build started, how long it took, and the server
  it ran on. Pick a row to open its log.
- **Deployment logs** — build output for the selected (or latest) deployment,
  live over SSE while the build runs, with find-in-logs, download and refresh
  controls.
- **Runtime logs** — the container's stdout/stderr, streaming live with
  **Stream live**, a tail selector (100 / 500 / 2000 / all), find-in-logs and
  download.
- **Networking** — the container port the proxy routes to, published
  `hostPort:containerPort` mappings, and the container labels the next
  deployment attaches (read-only).
- **Runtime** — CPU and memory ceilings for the container, and the
  pre/post-deployment command hooks.
- **Danger zone** — deletes the application: the container is stopped and
  removed, the configuration and history go with it, volumes stay on disk.
- The header's **Actions** menu redeploys with or without cache, restarts and
  stops the application.
