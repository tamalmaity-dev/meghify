# Install, update and uninstall on a VPS

This is the full reference for `scripts/install.sh` and the `meghify` helper
command it installs. The short version lives in the README; everything on
this page is what the installer actually does.

---

## One-liners

```bash
# follow the main branch (always the newest installer)
curl -fsSL https://raw.githubusercontent.com/tamalmaity-dev/meghify/main/scripts/install.sh | bash

# pinned release: this copy always matches the newest published release,
# because CI stamps it with the release commit
curl -fsSL https://github.com/tamalmaity-dev/meghify/releases/latest/download/install.sh | bash
```

Both work identically. Entrypoint arguments go through the pipe:

```bash
curl -fsSL .../install.sh | bash -s -- --verbose
curl -fsSL .../install.sh | PANEL_DIR=/srv/meghify APP_PORT=8443 bash
```

---

## What the installer does, in order

1. **Preflight** — inspects the machine and reports each check individually.
   It aborts *before touching anything* when a blocking problem exists.
2. **Docker** — installs Docker through `get.docker.com` when it is missing
   (covers Ubuntu, Debian, Fedora, Rocky/Alma, Arch, Alpine).
3. **Fetch** — downloads the newest release binary from GitHub Releases. When
   no release exists (or the tarball does not match the host architecture) it
   falls back to compiling the source snapshot inside Docker. `--from-source`
   instead builds on the host with git + Go 1.26+ + Node 20+.
4. **Configuration** — writes `/opt/meghify/.env` with a generated
   `AUTH_SECRET` (first run only; re-runs keep your `.env` untouched).
5. **Start** — systemd mode writes `meghify.service`, enables and starts it;
   Docker fallback runs `compose up -d --build`.
6. **Health wait** — polls `http://127.0.0.1:<port>/api/health` for up to
   120 seconds.
7. **Final link** — resolves the server's public IP (ifconfig.me → ipify →
   icanhazip → `hostname -I` fallback) and prints a bordered box with a
   clickable `http://<ip>:<port>` panel URL.
8. **Helper CLI** — installs the `meghify` management command to
   `/usr/local/bin/meghify`.

---

## Preflight checks

| Check | Warn | Blocks install |
|---|---|---|
| Linux kernel + distro (`/etc/os-release`) | — | non-Linux OS |
| architecture: amd64, arm64, armv7 | — | anything else |
| root user | message tells you the sudo form | not root |
| curl or wget present | — | neither |
| memory (`/proc/meminfo`) | 450–699 MB `works, but 1 GB+ is better` | under 450 MB |
| free disk on `$PANEL_DIR` | 1–2 GB | under 1 GB |
| `$APP_PORT` free (`ss`/`netstat`) | — | port taken |
| ports 80/443 busy | warns Traefik may not bind them | no |
| Docker engine running | auto-installs Docker instead; if the daemon stays down *warns* but installs the panel anyway | only the source fallback requires Docker |
| docker compose plugin | falls back to legacy `docker-compose v1` | only the source fallback |
| git | — | only `--from-source` |
| Go 1.26+ / Node 20+ / npm | — | only `--from-source` |

The summary line when something blocks the install:

```
✘ preflight found N blocking problem(s) — fix them and re-run
```

---

## Commands and flags

```text
install.sh [install] [--from-source] [--yes] [--verbose]
install.sh --uninstall [--yes] | --update | --version
```

| Argument | Meaning |
|---|---|
| *(none)* / `install` | install the latest release as a systemd service |
| `--uninstall` | stop the panel and remove its data, unit file, CLI and every Docker resource it created |
| `--update` | fetch the newest release and restart (with health-check rollback) |
| `--version` | report installer stamp, binary version, running version, latest release |
| `--from-source` | skip releases: clone and build on the host (systemd mode) |
| `--yes`, `-y` | auto-answer prompts (required for `--uninstall` inside pipes) |
| `--verbose` | stream command output instead of hiding it |
| `--help` | usage |

Environment overrides (set before `bash` in a pipe):

| Variable | Default | Effect |
|---|---|---|
| `PANEL_DIR` | `/opt/meghify` | install location; disk check runs here |
| `APP_PORT` | `8080` | public panel port |
| `PANEL_VERSION` | `latest` | pin a tag, e.g. `PANEL_VERSION=v0.1.0` |

A re-run reads `APP_PORT` back from the existing `.env`, so the printed link
always matches the running panel.

---

## Install modes

| | Default (release binary) | Source fallback | `--from-source` |
|---|---|---|---|
| Binary source | GitHub release asset | compiled inside Docker | compiled on the host |
| Runs as | `meghify.service` (systemd) | compose container | `meghify.service` |
| Host needs | Docker (for app deploys) | Docker + compose | git, Go 1.26+, Node 20+, npm |
| Speed after install | seconds | minutes (Docker build) | minutes |

Docker is still installed in every mode because app deploys always need an
engine on the server; only the panel's *own process* runs without it.

---

## What the installer leaves behind

```text
/opt/meghify/
  meghify              the panel binary (release and --from-source modes)
  .env                 AUTH_SECRET, APP_ENV, APP_PORT, PANEL_PUBLIC_URL,
                       PANEL_ADDR, DATA_DIR (+ DOCKER_HOST in Docker mode)
  data/                SQLite database, build logs, deploy workspaces
  docker-compose.yml   copy included in release tarballs (used by the
                       Docker fallback, harmless otherwise). Since 0.6.2 the
                       compose file no longer includes a Traefik service: the
                       panel deploys and owns the proxy itself. If an older
                       install still has the compose Traefik running on ports
                       80/443, remove it once: `docker compose -p meghify up
                       -d --remove-orphans` (or
                       `docker rm -f $(docker ps -aqf name=traefik)`)
  .env.example         reference copy
  repo/                only in the Docker source fallback: the source tree
/etc/systemd/system/meghify.service    in systemd modes
/usr/local/bin/meghify                 the management CLI
```

No other database or cache is needed. Everything is one static Go binary.

---

## The `meghify` helper

| Command | What it does |
|---|---|
| `meghify status` | `systemctl status meghify`, or `compose ps` in Docker mode |
| `meghify logs` | `journalctl -u meghify -f -n 200`, or `compose logs -f` |
| `meghify restart` | restarts the service or the compose project |
| `meghify version` | binary version, running API version, latest release |
| `meghify update --check` | prints whether a newer release exists (no changes) |
| `meghify update` | upgrade + restart + health check |
| `meghify uninstall [--yes]` | remove everything |

`--check` output is one of:

```text
up to date (v0.2.0)
an update is available: v0.2.0 (installed: 0.1.0)
installed 0.3.0 is newer than release v0.2.0
```

### How `meghify update` behaves

- **systemd mode** — downloads `meghify-linux-<arch>.tar.gz` from the latest
  release, binary-swaps, restarts, then polls health. If the new binary does
  not become healthy it restores the previous binary automatically (`meghify.old`)
  and restarts again — you are never left with a broken panel.
- **Docker mode** — re-downloads the release-tagged source, rewrites `repo/`,
  runs `compose up -d --build`, then polls health.

Version comparisons tolerate the `v` prefix and `dev` installs (anything not
parseable is treated as "older than every release").

---

## Uninstall

```bash
meghify uninstall           # interactive confirmation
meghify uninstall --yes     # non-interactive
curl -fsSL .../install.sh | bash -s -- --uninstall --yes
```

This is destructive: application data, database volumes, proxy certificates
and build cache are removed. Take a backup before running it.

Removed:

- the `meghify.service` unit and its `journalctl` entries
- `$PANEL_DIR` including `.env`, the data directory and `repo/`
- `/usr/local/bin/meghify`
- the panel's Compose project (including the `meghify-data` volume)
- every container created by Meghify: applications, databases, services,
  proxy, build sandbox, terminal helper and build helpers
- every Compose stack created for applications and services
- every Meghify-managed and Compose-project volume: application data, database
  data, the `meghify-build-cache` volume, proxy routing config and certificates
- every Meghify-managed and application/service network
- application images stamped with `meghify.application`, including old release
  tags, plus the panel, helper, proxy, sandbox and terminal images
- the Docker build cache

Kept:

- the Docker engine itself and its daemon configuration
- containers, images, volumes and networks that are not linked to Meghify
- unrelated projects and reverse-proxy configuration outside Meghify's labels

The result is effectively a fresh Docker host for this panel: the next install
does not inherit Meghify's old applications, images, volumes or build cache.

`--uninstall` refuses in a non-interactive pipe unless `--yes` is passed, so a
bare `curl | bash -s -- --uninstall` can never nudge the server by accident.

---

## Version reporting

```bash
meghify --version            # binary build info (exit 0, no side effects)
meghify version              # binary + running API + latest release
install.sh --version         # installer stamp + the same three lines
```

The installer stamp is the short commit SHA baked in by the release workflow;
a copy fetched straight from `main` reports `source (main branch)`.

---

## Troubleshooting

| Symptom | Cause and fix |
|---|---|
| `port 8080 is already in use` | something else (not Meghify) listens there — re-run with `APP_PORT=<other>` (or edit `.env` and restart). A re-run over an existing Meghify install recognizes its own panel and proceeds |
| `the panel installs anyway, app deploys wait` | Docker daemon down: `journalctl -u docker`; re-run the installer after fixing |
| panel not healthy after install | `journalctl -u meghify` (systemd modes) or `docker compose -p meghify logs` (Docker mode) |
| release tarball download fails | GitHub API rate limit (60 req/h per IP) — usually transient, retry in ~1 h; the installer falls back to a source build meanwhile |
| update failed the health check | previous binary was restored automatically; check `journalctl -u meghify`, then `meghify update` again |
| wrong link in the final box | `.env`'s `PANEL_PUBLIC_URL` is informational; set the real domain for Traefik routing |

Registration is closed once the first admin exists (`ALLOW_SIGNUP=true`
reopens it). Losing `AUTH_SECRET` means every stored SSH key, API token and
TOTP seed must be re-entered, so back `.env` up with the rest of the machine
state.
