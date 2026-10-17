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
4. **Release** — create the container with its environment variables, volumes
   and proxy labels, start it, wait for the health check when one is set, and
   retire the previous container only after the replacement is healthy.

Progress streams to the deployment log (durable, replayable) and to open
browsers over SSE.

## The build helper container

The panel itself is deliberately minimal: its image is distroless and carries
neither git nor nixpacks. Every git, buildpack and compose command runs inside
a disposable helper container that mounts the engine socket — the same model
Coolify uses. Two consequences:

- the host running the panel needs only Docker; and
- build tools never bloat the panel's own image or host.

The image is configured with `BUILD_HELPER_IMAGE` (default
`coollabsio/coolify-helper:1.0.17`). Point it at your own image if you prefer;
it needs `git`, `nixpacks`, `railpack` and the `docker` CLI on `PATH`.
Helper containers are named `meghify-builder-…` and are removed when the
deployment ends. If the image cannot be pulled, the panel falls back to
running the tools on its own host, which keeps older installs with those
tools working.

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
- **Logs** — build output for the selected (or latest) deployment, live over
  SSE while the build runs, with find-in-logs, download and refresh controls.
