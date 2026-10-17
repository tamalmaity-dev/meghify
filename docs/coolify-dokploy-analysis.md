# How Coolify and Dokploy build and route

A study of the two reference panels this project learns from. Everything here was
read directly from their repositories (`coollabsio/coolify` and `Dokploy/dokploy`,
`main` branches) on 2026-10-18, during the work that produced v0.6.1. The goal:
record exactly what they made — proxy/domain management and the build system
(nixpacks, railpack, Dockerfile, static) — then compare it with Meghify so the
deltas are obvious.

---

## Coolify (coollabsio/coolify)

### Shape

- Laravel/PHP monolith. Almost the entire deployment engine is one queue job:
  `app/Jobs/ApplicationDeploymentJob.php` (~300 KB).
- Multi-server first-class: the panel SSHes into arbitrary servers and runs
  Docker CLI there (`instant_remote_process`). Docker Compose for normal
  servers, `docker stack deploy` for swarm managers.
- The proxy is configured **per server** (`Server::proxyType()`), not per app.

### Proxy and domains

| Piece | What it does |
|---|---|
| `app/Enums/ProxyTypes.php` | `NONE / TRAEFIK / NGINX / CADDY` — Traefik is the default; the others are real alternates. |
| `app/Actions/Proxy/StartProxy.php` | Writes the compose file under `/data/coolify/proxy/` (plus a `dynamic/` folder), then `docker compose up -d --wait --remove-orphans`; on swarm managers `docker stack deploy -c docker-compose.yml coolify-proxy`. Old proxy is stopped/removed first with a bounded wait loop. |
| `app/Actions/Proxy/CheckProxy.php` | Renewal gate: skips when already running; **parses the ports from the proxy config (`ProxyPortParser`) and probes them in parallel for conflicts before starting** — refuses to start on conflict with a UI-friendly error. |
| `bootstrap/helpers/proxy.php` (37 KB) | Default proxy config generator (`generateDefaultProxyConfiguration`), access-log analytics flags (`--accesslog.format=json` with Cloudflare header keeping), flag add/remove management, legacy dashboard-label cleanup. |
| Traefik version checking | `CheckTraefikVersion*` jobs + emails/Telegram notifications when Outdated (migrations dated 2025-11). Certificate resolver became a server setting (`traefik_certificate_resolver`, migrations 2026-10). |

**How apps reach the proxy:** the proxy container is connected to **every user
network** on that server — `connectProxyToNetworks($server)` emits a
`docker network connect <net> coolify-proxy || true` per destination network.
Applications themselves get **Traefik routing labels generated on the
container** (by `App\Services\ConfigurationGenerator`), matching the server's
proxy type. From the Application model:

> "Proxy labels are generated for the primary server, so Traefik labels do not
> work on a Caddy server and the reverse."

Domains are comma-separated FQDNs on the application, validated by
`isValidDomainUrl` in `bootstrap/helpers/domains.php`, with `custom_labels`
passthrough, a `www / non-www` redirect enum, and Dockerfile-declared
healthcheck detection (`custom_healthcheck_found`).

### Builds

Each deployment starts a **helper container on the application's own network**,
with the host Docker socket mounted — builds are Docker CLI calls against the
**host engine**, from inside that helper:

```
docker run -d --network <destination network> --name <deployment_uuid> \
  -v ~/.docker/config.json:/root/.docker/config.json:ro \
  -v /var/run/docker.sock:/var/run/docker.sock <helperImage>
```

`DOCKER_CLIENT_*` env keys (`DOCKER_HOST`, `DOCKER_CONFIG`, `DOCKER_BUILDKIT`,
`BUILDX_*`, …) are passed through into the helper. On a dedicated build server
the helper gets no `--network` at all. There is **no isolation and no resource
cap** on builds anywhere — long builds eat the host's CPU. (This is exactly the
72% CPU behaviour observed on a Coolify-class panel; Meghify's sandbox caps it.)

**BuildKit/buildx probing:** before building, Coolify tests
`docker build --help | grep -q --progress` for BuildKit support and builds an
`--builder coolify-*` buildx builder when buildx exists, with plain fallbacks.

**Build-time DNS — `addHostFlags()`:** every `docker build` call carries
`--add-host name:ip` entries for the containers on the destination network:

> "Build helper containers are named after their deployment uuid and only live
> for the duration of that deployment. BuildKit keys every RUN layer on the
> --add-host set, so letting one in gives the next build a different set and
> forces a full rebuild."

So their helpers are filtered out of the set (by deployment uuid) and the IPs
of the *application* containers stay stable, keeping BuildKit layer caching
intact. This is the same gap (build steps cannot resolve the app's service
names) that Meghify fixed in v0.6.1.

**Buildpacks** (in `ApplicationDeploymentJob`, buildpack enum `BuildPackTypes`):

| Pack | What runs |
|---|---|
| nixpacks | Two-step: custom plan at `/artifacts/thegameplan.json`, then `nixpacks build -c <plan> --cache-key '<app uuid>' --no-error-without-start -n <build image> <workdir> -o <workdir>` (generate-only), then `cat <workdir>/.nixpacks/Dockerfile` into the log and **its own `docker build`** from the generated Dockerfile — `--progress plain`, with `--add-host` flags, `--network host`, cache args and build args. Build-time variables pass as `--env K=V` plus injected `COOLIFY_*` build-time vars. The UI ships a "nixpacks deprecated" callout. |
| railpack | Repository config `railpack.json` / generated `.coolify/railpack.generated.json`; a named buildx builder (`--driver docker-container`); plan + image built by `docker buildx build --builder coolify-railpack --add-host … --network host --build-arg BUILDKIT_SYNTAX=… --build-arg cache-key=<uuid> -f /artifacts/railpack-plan.json --progress plain --load -t <image> <workdir>`, with build-secret flags. |
| Dockerfile | Configurable `dockerfile_location` + optional target stage; **Coolify rewrites the Dockerfile** (`modify_dockerfile_for_secrets`) to add `# syntax` and `RUN --mount=type=secret` build secrets; build args appended; `--progress plain` under BuildKit. |
| static | `publish_directory` → a static image from an nginx (or caddy) base, with optional custom nginx configuration injected; same `--add-host --network host` treatment. |
| docker-compose | Repository `docker-compose.yml` with custom build command support. |
| docker image | Deploy a prebuilt image straight (`dockerImage`/`dockerImageTag` on the queue row). |

**Caching:** docker builds get `--build-arg cache-key='<app uuid>'` plus
`--build-arg secrets-hash=<hash of env vars>`; `force_rebuild` turns into
`--no-cache`; a newer `disable_build_cache` setting forces it permanently.

**Teardown & controls:** the job's default timeout is 3600 s and each server
carries a `dynamic_timeout` setting; cancellation is a queue status
(`CleanupCancelledDeployment`); everything ends in
`graceful_shutdown_container($this->deployment_uuid, skipRemove: …)` in a
finally path.

### Release and health

`health_check()` polls the *new* container's Docker health status and gates the
swap on `newVersionIsHealthy`:

- User healthcheck configured **or** a `HEALTHCHECK` in the Dockerfile → poll
  `docker inspect` until `healthy`; `unhealthy` fails the deployment.
- **No healthcheck at all → `newVersionIsHealthy = true` immediately.**

That last case is the hole Meghify hit in the field: a container that crashes
on start is still "released", the application says *running*, and the domain
serves the proxy's bare `404 page not found` while nothing runs. Coolify has
not closed this (as of this study). Rollback is a queue-level flag
(`rollback`) that redeploys an earlier commit's image; `restart_only`
re-runs the old container without rebuilding.

---

## Dokploy (Dokploy/dokploy)

### Shape

- Next.js server + TypeScript `packages/server` monorepo; runs its panel on a
  **swarm manager** node with `/var/run/docker.sock`.
- Two application types: swarm-orchestrated services (apps, databases,
  compose) and a plain-docker "docker" type on the shared `dokploy-network`.
- Builders do not run in containers: they **emit bash strings that execute on
  the host** (a nixpacks/railpack build can even `sudo bash -c "$(curl -fsSL
  https://railpack.com/install.sh)"`).

### Proxy and domains

Single proxy: Traefik, deployed and configured by
`packages/server/src/setup/traefik-setup.ts`, which writes
`/etc/dokploy/traefik/traefik.yml`:

```yaml
providers:
  swarm:  { exposedByDefault: false, watch: true }
  docker: { exposedByDefault: false, watch: true, network: "dokploy-network" }
  file:   { directory: "/etc/dokploy/traefik/dynamic", watch: true }
entryPoints:
  web:      { address: ":<TRAEFIK_PORT>" }
  websecure: { address: ":<TRAEFIK_PORT>" }
```

Three routing mechanisms coexist:

1. **File provider — the primary one.** `utils/traefik/application.ts` writes
   one `dynamic/<appName>.yml` per application
   (`writeTraefikConfig`/`writeTraefikConfigRemote`, local or over SSH); each
   Domain row has a `uniqueConfigKey` and becomes an HTTP router named
   `${appName}-service-<key>` with `passHostHeader: true`. `manageDomain()`
   (`utils/traefik/domain.ts`) adds or removes a domain's router; **a disabled
   domain is fully removed from routing**. TLS: `certResolver: "letsencrypt"`
   by default, an **arbitrary `domain.customCertResolver`** per domain, or
   none. Because Traefik watches the folder, TLS/redirect/path changes apply
   hot, without redeploying the container.
2. **Compose labels.** Compose-type apps are decorated with
   `traefik.http.routers.{appName}-{uniqueConfigKey}-*` labels at compose
   decorate time (`utils/docker/domain.ts`, which also owns
   `removeDomainLabels` with per-router/service/middleware prefixes and compose
   randomization for name collisions).
3. **Swarm provider labels** for swarm services (router/service/middleware
   labels on the service's `deploy.labels`).

Middleware modules under `utils/traefik/`: `middleware.ts` (stripprefix /
addprefix path handling), `redirect.ts` (www/non-www), `security.ts` (basic
auth & friends), `forward-auth.ts` (proprietary), `web-server.ts`,
`file-types.ts`, `types.ts`. Domains are first-class DB rows edited via
`services/domain.ts` + a tRPC domain router.

### Builds

`packages/server/src/utils/builders/` — each builder returns a bash command:

| Pack | What runs |
|---|---|
| nixpacks (2 KB) | One-step: `nixpacks build <src> --name <app> --env K=V … [--no-cache] [--no-error-without-start]` — nixpacks itself drives docker. For `publishDirectory`: `docker create --name <app>-<nanoid10> <image>` → `docker cp <cid>:/app/<publish-dir>/. <path>` → `docker rm` → then a static nginx build of the copied artifacts. |
| railpack | Installs the railpack CLI on the host if missing; `docker buildx create --name <builder> --driver docker-container`; `railpack prepare` → plan; `docker buildx build` with the railpack frontend (`-f <plan>`, secrets exported as env); `docker buildx rm <builder>` afterwards. |
| Dockerfile (`docker-file.ts`) | `docker build` with build args, build secrets, optional target stage (`dockerBuildStage`), `cleanCache`; build envs are dumped as a `.env` next to the context (`createEnvFileCommand`: base64 echo, from `builders/utils.ts`). |
| static | Static site containers (nginx), plus assets helpers. |
| heroku | Heroku buildpacks CLI. |
| paketo | `pack` CLI (Cloud Native Buildpacks). |
| compose | `docker compose` deployment of the repository compose file. |

Notes: no helper container, no `--add-host` concept (build steps never get the
app network's service names), no BuildKit probing, no cache keys beyond the
builders' own caches. Builds also have **no resource limits**.

### Deploy and health

- Apps deploy as **swarm services**; service upsert includes Docker health
  checks and `UpdateConfig` (rolling-update semantics with rollback on failure
  handled by the swarm), plus a `services/rollbacks.ts` feature.
- `packages/server/src/utils/startup/cancel-deployments.ts`: on panel boot,
  deployments left `in progress` by a crash are cancelled — same recovery idea
  as Meghify's v0.6.0 restart recovery.
- A deployment queue worker (`apps/dokploy/server/queues/deployments-queue.ts`)
  serializes deployments per application.

---

## Side-by-side

### Runtime & routing

| | Coolify | Dokploy | Meghify (v0.6.1) |
|---|---|---|---|
| Stack | Laravel + queue workers, SSH remote-exec | Next.js + TS, docker.sock on manager node | Go, single binary |
| Proxy engines | Traefik / NGINX / Caddy (per server) | Traefik only | Traefik v3 |
| App routing | Container labels (per proxy type) | File provider per app + compose labels + swarm labels | Container labels |
| Hot re-route without redeploy | No (labels change on redeploy) | **Yes** (file provider) | No (labels) |
| Proxy connected to app networks | Every user network via `docker network connect` | Apps on `dokploy-network`, provider pinned | App joins `meghify` |
| Auto domain | None | None | sslip.io auto-domain |
| TLS | Let's Encrypt via resolver + custom cert resolvers per server | letsencrypt / **custom resolver per domain** / none | letsencrypt resolver when `ACME_EMAIL` set |
| Middlewares | www redirect enum, custom labels | stripsuffix/prefix, redirects, basic auth, forward-auth | gzip, redirect-to-https, path rules, robots tag |
| Port-conflict check before proxy start | Yes (`CheckProxy` + port parser) | No equivalent | Yes (proxy start reports port take-over) |
| Proxy lifecycle care | stop → remove → wait loop → up with `--wait` | static config + fixed container | on-demand ensure + recreate |

### Build system

| | Coolify | Dokploy | Meghify (v0.6.1) |
|---|---|---|---|
| Build execution | Helper container on app network + host engine via docker.sock | Plain bash on the host | Helper container → **DinD sandbox** against the sandbox engine |
| Build isolation / CPU cap | None | None | **Yes** (half the node) |
| nixpacks | Two-step (`-o <workdir>` → own `docker build` of `.nixpacks/Dockerfile`), plan file, `--cache-key` | One-step (nixpacks drives docker) | Two-step, Coolify-style (`--out` → own build) |
| railpack | buildx builder `coolify-railpack`, plan + `-f` frontend, secrets flags | host CLI install (sudo curl) + buildx frontend | `railpack build <src> --name <ref>` |
| Dockerfile | location/target, **→ build-secrets Dockerfile rewrite** | build args, secrets, target, `.env` dump | build args, labels, sandbox/co-build |
| static (`publish_dir`) | Yes (nginx/caddy image) | Yes (`docker create`/`cp` → nginx) | No (roadmap candidate) |
| heroku/paketo packs | No | Yes | No |
| Build-time service DNS | `--add-host name:ip` + `--network host`, helpers filtered out | None | `--add-host name:ip` (same solution, v0.6.1) |
| Cache strategy | `--cache-key`/plan cache + `secrets-hash`; `force_rebuild`/`--no-cache` | builder-native | cache mount keyed per application |
| BuildKit probing | Yes (`--progress` probe) + buildx builder | No | BuildKit env set, engine-known version |
| Secrets into builds | Dockerfile `--mount=type=secret` rewrite | build secrets / env file | env-backed build args (registry creds redacted) |
| Timeout / cancel | `dynamic_timeout` per server; cancel status | queue worker controls | `DEPLOY_TIMEOUT` + **Kill build button** |
| Build teardown | `graceful_shutdown_container` in finally | builder manages nothing extra | helper removed every path, restart orphans swept |

### Release & resilience

| | Coolify | Dokploy | Meghify (v0.6.1) |
|---|---|---|---|
| Health gating | inspect-poll until `healthy`; **no healthcheck ⇒ healthy immediately** | swarm health checks + `UpdateConfig` | healthcheck poll **and** `confirmStartup` when no healthcheck: dead-at-start / restart-loop fails the release |
| Crash-at-start can be "released" | **Yes** (the bare 404 class of bug) | Partially (swarm retries) | **No** — release reverts |
| Failure output in the deployment log | Limited | Swarm events | **Tail of the container's logs dumped** into the log before revert |
| Rollback | Queue `rollback` flag to an older commit | `rollbacks.ts` | redeploy accepted image (rolling swap) |
| Boot recovery | Cancel/cleanup actions | `cancel-deployments.ts` on startup | in-flight releases cancelled on restart, reason recorded |
| Zero-downtime | With healthcheck + single-domain setups | Swarm native | swing release; host-port apps degenerate to stop/start |

### Verdict scorecard

| Axis | Winner | Because |
|---|---|---|
| Portability / multi-server | Coolify | SSH remote-exec to any provider |
| Lean single-node footprint | Dokploy | one Node process; no Laravel worker fleet |
| Feature breadth | Coolify | templates, databases, previews, cloud, MCP tools |
| Proxy architecture | Dokploy | file provider = hot, redeploy-independent routing |
| Domain/feature UX per domain | Dokploy | TLS mode, resolvers, middlewares as first-class rows |
| Operational defensiveness of builds | Coolify | probing, cache keys, add-host, graceful teardown |
| Resource-safety of builds | **Meghify** | only implementation that caps and isolates builds |
| Dead-at-start release hole | **Meghify** | `confirmStartup` + log dump + revert (both others share the hole to their degree) |

---

## Ideas worth stealing (roadmap candidates)

1. **File-provider dynamic domains** (Dokploy) — write `dynamic/<app>.yml`
   beside the label path so TLS/redirect/path edits apply without a redeploy.
2. **Per-domain custom certificate resolver** (Dokploy's `customCertResolver`).
3. **`publish_directory` static buildpack** (both) — copy artifacts out, serve
   with a static nginx image.
4. **Build secrets as `--mount=type=secret`** via Dockerfile rewrite (Coolify's
   `modify_dockerfile_for_secrets`) for tokens that must not persist in layers.
5. **`secrets-hash` cache busting** (Coolify) — rebuild when env vars change.
6. **www/non-www redirect + basic-auth middlewares** (both) as domain options.
7. **Zero-downtime healthcheck sidecar** (Coolify) — check the replacement
   through a temp listener before swapping, for apps that cannot share a port.
8. **NGINX/Caddy proxy alternates** (Coolify) — optional second engine.
9. **Traefik version checker + access-log analytics flags** (Coolify).
10. **heroku/paketo builders** (Dokploy) — low priority vs the above.

And deliberately **not** adopted (from this study): builds against the host
engine without isolation (both), one-step nixpacks that hides the generated
Dockerfile (Dokploy), healthy-without-healthcheck releases (Coolify).

---

*Study material: Coolify `app/Jobs/ApplicationDeploymentJob.php`,
`app/Actions/Proxy/*`, `bootstrap/helpers/{proxy,applications,domains}.php`,
`app/Enums/ProxyTypes.php`; Dokploy `packages/server/src/utils/builders/*`,
`packages/server/src/utils/traefik/*`, `packages/server/src/utils/docker/domain.ts`,
`packages/server/src/setup/traefik-setup.ts`,
`packages/server/src/services/{domain,docker,application}.ts`,
`packages/server/src/utils/startup/cancel-deployments.ts`.*
