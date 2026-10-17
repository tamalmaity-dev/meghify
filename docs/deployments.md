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

## The build sandbox

A build executes arbitrary repository code (`npm ci`, compilers, arbitrary
`RUN` steps), and that code is what can take a small server down. Meghify
therefore runs Dockerfile, nixpacks and railpack builds inside an **isolated
Docker-in-Docker daemon** — the build sandbox — and not against the engine
that runs the panel and the applications:

```
panel ──> helper container ──> build sandbox (meghify-buildd, limited CPU/RAM)
                                     │  builds the image
                                     ▼
                              save + load ──> host engine ──> release
```

- The sandbox is a `docker:29-dind` container (override with
  `BUILD_SANDBOX_IMAGE`) holding a private image cache in the
  `meghify-build-cache` volume, on its own `meghify-build` network.
- It is **resource-limited**: `BUILD_CPU_LIMIT` and `BUILD_MEMORY_LIMIT`, and
  when unset, **half of the node** (at least one core and 1 GiB, at most four
  cores and 8 GiB) is reserved for builds. No matter what a build does, the
  panel, the proxy and the running applications keep their share. The limits
  are logged at sandbox creation; recreating the container (`docker rm -f
  meghify-buildd`) re-reads them after a configuration change.
- The built image is transferred to the host engine with a compressed
  `docker save`/`docker load` before the release step.
- Docker Compose stacks are the exception: `docker compose up` must create
  the application containers on the host engine, so compose deployments keep
  the mounted engine socket.
- `BUILD_SANDBOX` selects the behavior: `auto` (default) falls back to
  the legacy socket-mounted helper when nested Docker is not permitted, `on`
  makes a sandbox failure an error, `off` disables the sandbox entirely.
- **Builds can reach sibling services by name.** A build process cannot use
  user-defined-network DNS (there is no network attached to a build), so
  `next build` running against a database service would fail to resolve its
  host. Before every Dockerfile and nixpacks build the panel assembles the
  `--add-host name:ip` entries of the application's private network — the
  same mapping Coolify passes to each build — so code that renders against a
  real service during prerendering works.
- Nixpacks builds go through the same two steps Coolify uses:
  `nixpacks build --out` only generates the Dockerfile, and the panel then
  runs `docker build` itself — that is why the host entries and the image
  labels above are possible at all.

## The build helper container

The panel itself is deliberately minimal: its image is distroless and carries
neither git nor nixpacks. Every git, buildpack and compose command runs inside
a disposable helper container — the same model Coolify uses. Two consequences:

- the host running the panel needs only Docker; and
- build tools never bloat the panel's own image or host.

The image is Meghify's own — `ghcr.io/tamalmaity-dev/meghify-helper:<version>`,
published by every release (Alpine plus git, nixpacks, railpack, and the
docker CLI with buildx and compose). `BUILD_HELPER_IMAGE` overrides it if you
want to pin your own image; it needs `git`, `nixpacks`, `railpack` and the
`docker` CLI on `PATH`. Helper containers are named `meghify-builder-…` and
are removed when the deployment ends, when it is killed, when it times out,
and when the panel restarts. If the image cannot be pulled, the panel
falls back to running the tools on its own host, which keeps older installs
with those tools working.

### Keeping builds light

Builds are CPU-heavy, so at most `BUILD_CONCURRENCY` deployments (default
**1**) build at the same time — a deployment that has to wait says so in its
log. The sandbox caps every build's CPU and memory (see above); the helper
container inherits the same caps.

### Stuck builds cannot hang the panel

A deployment is bounded by `DEPLOY_TIMEOUT` (default **30m**, covering build,
image transfer, release and health checks). Coolify uses the same idea (a
one-hour queue job timeout, configurable per server). When the deadline hits,
the helper container is removed — which kills the build process with it — and
the deployment fails with a message in its log. A running build can also be
killed from the UI: open the deployment's build output and press
**Kill build** (the endpoint is `DELETE /api/deployments/{id}`).

If the panel itself is killed mid-deployment (OOM, reboot, `systemctl
restart`), startup recovery marks the in-flight deployment **canceled**
("interrupted by a panel restart"), restores the application's status from its
container, removes orphaned helpers and deletes half-created rolling-release
containers — so the next deploy is never blocked by a stuck row. Dokploy does
the same at boot (`initCancelDeployments`).

## Every release is confirmed

The rolling release starts the replacement first, health checks it (when the
application has a health check, for 90s), retires the old container only
afterwards, and reverts on failure — the domain never serves a half-released
application. Containers **without** a health check get a short startup
confirmation instead: a container that exits inside the first seconds (a
missing environment variable, an OOM kill, a broken start command), or one
whose restart policy is already counting restarts, fails the deployment. The
container's own recent output is copied into the deployment log, so the
failure explains itself without a trip to the server shell — and the previous
deployment keeps running.

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

Nixpacks gets every `build`-scope environment variable as `--env`, and
defaults `NIXPACKS_NODE_VERSION` to **22** (`BUILD_DEFAULT_NODE_VERSION`)
unless the application sets it. Without that default nixpacks falls back to
the end-of-life Node 18 whenever `package.json` has no `engines` field, which
breaks current Next.js dependencies.

## Automatic domains

An application created without a domain gets one:
`<name>-<id>.<server-ip>.sslip.io`, served over plain HTTP. sslip.io resolves
any subdomain that carries an IP back to that IP, so the app is reachable
immediately with no DNS work — replace it with a real domain any time from
the application's **Domains** card.

## Domains

Each domain carries its own routing options, edited from the Domains card:

| Option | Meaning |
|---|---|
| **Protocol** | `https` generates a TLS router plus a plain-HTTP companion; `http` routes plain traffic only |
| **Internal port** | overrides the application port for this domain |
| **Path** | a path prefix (e.g. `/api/v3`) the domain routes |
| **Protocol redirect** | for TLS domains: send `http://` to `https://` (the app-level **Redirect HTTP to HTTPS** switch sets the default and applies to existing domains) |
| **Search indexing** | off adds an `X-Robots-Tag: none` middleware |
| **DNS status** | **Check All DNS** (or the row's check) resolves the host and compares it with the server's public address: *DNS matches*, *DNS mismatch* or *Not checked* |

Routing labels are generated per domain in the Coolify layout — paired
`http-<app>-<n>` / `https-<app>-<n>` routers with per-domain services, `gzip`,
the `redirect-to-https` middleware and, when `ACME_EMAIL` is configured, the
`letsencrypt` certificate resolver. Re-deploy the application to attach label
changes to the running container.

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

The proxy's entrypoints are named `http` (port 80) and `https` (port 443),
matching the router labels.

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
- **Danger zone** — a two-step delete: pick the cleanup options (volumes,
  networks, server configuration files, Docker cleanup of unused images and
  the builder cache), then type the resource name to confirm. The container is
  stopped and removed, the volume records go with the application, and the
  selected resources are cleaned up.
- The header's **Actions** menu redeploys with or without cache, restarts and
  stops the application.

## See also

An in-depth study of how Coolify and Dokploy build and route — proxies,
domains, nixpacks/railpack/Dockerfile/static builds, health gating — with
side-by-side comparisons against Meghify, lives in
[coolify-dokploy-analysis.md](coolify-dokploy-analysis.md).
