#!/usr/bin/env bash
# One-line installer for Meghify, shaped like the Coolify and Dokploy installers.
#
#   curl -fsSL https://raw.githubusercontent.com/tamalmaity-dev/meghify/main/scripts/install.sh | bash
#
# Preflight checks everything that matters first (Linux kernel, distro,
# architecture, root, curl/wget, memory, disk, chosen port, Docker engine and
# the Go/Node toolchain only when a host build is requested) → installs
# Docker when it is missing → fetches the latest release binary from GitHub
# and runs it under systemd (the binary comes from GitHub Actions; when no
# release exists, the source is compiled inside Docker, or on the host with
# --from-source) → writes .env → starts the panel → waits for the API to
# answer → prints a clickable link to the web panel.
#
# Commands: (default) install | --uninstall | --update | --version
# Flags:    --from-source, --yes, --verbose, --help
# Env:      PANEL_DIR, PANEL_VERSION, APP_PORT pass through a pipe as
#             curl -fsSL ... | PANEL_DIR=/srv/meghify APP_PORT=8443 bash
#
# Once a release exists, the pinned copy also works (always matches the
# newest release):
#   curl -fsSL https://github.com/<repo>/releases/latest/download/install.sh | bash
set -euo pipefail

REPO="tamalmaity-dev/meghify"
REPO_URL="https://github.com/${REPO}"
PANEL_DIR="${PANEL_DIR:-/opt/meghify}"
PANEL_VERSION="${PANEL_VERSION:-latest}"
APP_PORT="${APP_PORT:-8080}"
FROM_SOURCE=0
VERBOSE=0
ASSUME_YES=0
MODE="install"             # install | uninstall | update | version
LICENSE_SPDX="Apache-2.0"
HEALTH_TIMEOUT=120
STAMP="__INSTALLER_STAMP__" # the release workflow bakes this build's commit
DOCKER_STATE="absent"      # absent | running | broken

c_info='\033[1;36m'; c_ok='\033[1;32m'; c_warn='\033[1;33m'
c_err='\033[1;31m'; c_off='\033[0m'
if [[ -t 1 ]]; then
  say()  { printf "${c_info}==>${c_off} %s\n" "$1"; }
  ok()   { printf "  ${c_ok}✔${c_off} %s\n" "$1"; }
  warn() { printf "  ${c_warn}!${c_off} %s\n" "$1"; }
  fail() { printf "  ${c_err}✘${c_off} %s\n" "$1"; }
  info() { printf "  · %s\n" "$1"; }
else
  say()  { printf '==> %s\n' "$1"; }
  ok()   { printf "  OK  %s\n" "$1"; }
  warn() { printf "  WRN %s\n" "$1"; }
  fail() { printf "  ERR %s\n" "$1"; }
  info() { printf "  ==  %s\n" "$1"; }
fi
die() { fail "$1" >&2; exit 1; }
run() { if (( VERBOSE )); then "$@"; else "$@" >/dev/null 2>&1; fi; }

print_banner() {
  # ANSI-Shadow "MEGHIFY" figlet art.
  if [[ -t 1 ]]; then printf "${c_info}"; fi
  cat <<'ART'
███╗   ███╗███████╗ ██████╗ ██╗  ██╗██╗███████╗██╗   ██╗
████╗ ████║██╔════╝██╔════╝ ██║  ██║██║██╔════╝╚██╗ ██╔╝
██╔████╔██║█████╗  ██║  ███╗███████║██║█████╗   ╚████╔╝
██║╚██╔╝██║██╔══╝  ██║   ██║██╔══██║██║██╔══╝    ╚██╔╝
██║ ╚═╝ ██║███████╗╚██████╔╝██║  ██║██║██║        ██║
╚═╝     ╚═╝╚══════╝ ╚═════╝ ╚═╝  ╚═╝╚═╝╚═╝        ╚═╝
ART
  if [[ -t 1 ]]; then printf "${c_off}"; fi
  echo
}

usage() {
  cat <<USAGE
  usage: install.sh [install] [--from-source] [--yes] [--verbose]
         install.sh --uninstall [--yes] | --update | --version

  commands
    (no args)      install the latest release binary under systemd; falls
                   back to compiling the source inside Docker when no
                   GitHub release exists
    install        same as running it with no arguments
    --uninstall    remove the panel, its data and the meghify CLI
    --update       fetch the newest release (or rebuild the compose stack)
                   and restart the panel
    --version      print the installer, binary and running versions

  flags
    --from-source  build with a host toolchain (Go 1.26+, Node 20+, git)
                   and run under systemd instead
    --yes | -y     answer prompts automatically (--uninstall in pipes)
    --verbose      stream every command instead of hiding its output

  env: PANEL_DIR (default /opt/meghify), PANEL_VERSION (latest), APP_PORT (8080)
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    install)       MODE="install" ;;
    --uninstall)   MODE="uninstall" ;;
    --update)      MODE="update" ;;
    --version)     MODE="version" ;;
    --from-source) FROM_SOURCE=1 ;;
    --yes|-y)      ASSUME_YES=1 ;;
    --verbose)     VERBOSE=1 ;;
    --help|-h)     usage; exit 0 ;;
    *)             usage >&2; die "unknown flag: $1" ;;
  esac
  shift
done

# ---------------------------------------------------------------------------
# shared helpers — versions, releases, health
# ---------------------------------------------------------------------------
fetch() { # fetch <url> <dest>; curl preferred, wget as fallback
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$1" -o "$2"
  else
    wget -qO "$2" "$1"
  fi
}

api_get() { fetch "$1" /dev/stdout 2>/dev/null || true; }

# ---------------------------------------------------------------------------
# display-IP resolution — the printed panel URL should use the server's public
# IPv4. Echo services are queried over IPv4 on purpose: on a dual-stack host a
# plain request may leave over IPv6 and report that address instead.
# ---------------------------------------------------------------------------
public_ip4() {
  local ip
  for src in "https://ipv4.icanhazip.com" "https://api.ipify.org" "https://ifconfig.me/ip"; do
    if command -v curl >/dev/null 2>&1; then
      ip="$(curl -4 -fsSL --max-time 5 "$src" 2>/dev/null | tr -d '[:space:]')" || ip=""
    elif command -v wget >/dev/null 2>&1; then
      ip="$(wget -q -4 -T 5 -O - "$src" 2>/dev/null | tr -d '[:space:]')" || ip=""
    fi
    if [[ "$ip" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]]; then
      printf '%s' "$ip"
      return 0
    fi
  done
  return 1
}

local_ip4() {
  local ip=""
  ip="$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for (i=1;i<=NF;i++) if ($i=="src") {print $(i+1); exit}}')" || ip=""
  if [[ -z "$ip" ]]; then
    ip="$(hostname -I 2>/dev/null | tr ' ' '\n' | grep -m1 -E '^([0-9]{1,3}\.){3}[0-9]{1,3}$')" || ip=""
  fi
  printf '%s' "$ip"
}

url_host() { # url_host <ip> — bracket IPv6 literals so URLs stay valid
  if [[ "$1" == *:* ]]; then printf '[%s]' "$1"; else printf '%s' "$1"; fi
}

resolve_host_ip() { # IPv4 first; IPv6 only when there is no IPv4 at all
  local ip=""
  ip="$(public_ip4)" || ip=""
  if [[ -z "$ip" ]]; then ip="$(local_ip4)" || ip=""; fi
  if [[ -z "$ip" ]]; then ip="$(hostname -I 2>/dev/null | awk '{print $1}')" || ip=""; fi
  printf '%s' "$ip"
}

ver_ge() { # ver_ge <version> <minimum> — drop the v prefix before calling
  printf '%s\n%s\n' "$1" "$2" | sort -V | head -n1 | grep -qx "$2"
}

env_get() { # env_get <file> <KEY> — crude .env lookup, robust to CR endings
  sed -n "s/^${2}=//p" "$1" 2>/dev/null | tail -n1 | tr -d '\r'
}

arch_name() { # release asset suffix for this machine
  case "$(uname -m)" in
    x86_64|amd64)  echo "amd64" ;;
    aarch64|arm64) echo "arm64" ;;
    armv7l|armv7)  echo "armv7" ;;
    *)             echo "" ;;
  esac
}

gh_real_latest() { # true latest release, ignoring any PANEL_VERSION pin
  sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' \
    <<< "$(api_get "https://api.github.com/repos/${REPO}/releases/latest")" | head -n1
}

gh_pinned_or_latest_tag() { # PANEL_VERSION pin wins so installs are reproducible
  if [[ "$PANEL_VERSION" == "latest" ]]; then
    gh_real_latest
  else
    printf '%s' "$PANEL_VERSION"
  fi
}

installed_version() { # empty when unknown
  local out=""
  if [[ -x "${PANEL_DIR}/meghify" ]]; then
    out="$("${PANEL_DIR}/meghify" --version 2>/dev/null | awk '{print $2}' || true)"
  fi
  if [[ -z "$out" && -f "${PANEL_DIR}/.env" ]]; then
    out="$(sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' \
      <<< "$(api_get "http://127.0.0.1:${APP_PORT}/api/version")" | head -n1)"
  fi
  printf '%s' "$out"
}

api_response_version() {
  sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' \
    <<< "$(api_get "http://127.0.0.1:${APP_PORT}/api/version")" | head -n1
}

compose_cmd() { # compose_cmd <args…> — prefer v2 plugin, fall back to v1
  if docker compose version >/dev/null 2>&1; then
    docker compose --project-directory "$PANEL_DIR" \
      -f "$PANEL_DIR/repo/docker-compose.yml" -p meghify "$@"
  elif command -v docker-compose >/dev/null 2>&1; then
    docker-compose --project-directory "$PANEL_DIR" \
      -f "$PANEL_DIR/repo/docker-compose.yml" -p meghify "$@"
  else
    return 127
  fi
}
dc() { compose_cmd "$@"; }

fetch_release() { # fetch_release <tag> <arch> — extracts meghify.new into
                  # $PANEL_DIR; returns non-zero instead of dying so callers
                  # can fall back to a source build
  local tag="$1" name="$2" stage
  stage="$(mktemp -d /tmp/meghify-rel.XXXXXX)"
  if ! fetch "${REPO_URL}/releases/download/${tag}/meghify-linux-${name}.tar.gz" \
    "$stage/rel.tar.gz"; then
    rm -rf "$stage"
    return 1
  fi
  if ! tar -xzf "$stage/rel.tar.gz" -C "$stage"; then
    rm -rf "$stage"
    return 1
  fi
  if [[ ! -f "$stage/meghify" ]]; then
    rm -rf "$stage"
    return 1
  fi
  mkdir -p "$PANEL_DIR"
  mv -f "$stage/meghify" "$PANEL_DIR/meghify.new"
  mv -f "$stage/docker-compose.yml" "$PANEL_DIR/docker-compose.yml" 2>/dev/null || true
  mv -f "$stage/.env.example" "$PANEL_DIR/.env.example" 2>/dev/null || true
  rm -rf "$stage"
}

run_health() { fetch "http://127.0.0.1:${APP_PORT}/api/health" /dev/null 2>/dev/null; }

wait_healthy() { # wait_healthy — returns non-zero on timeout
  say "waiting for the API to answer (up to ${HEALTH_TIMEOUT}s)"
  local deadline=$((SECONDS + HEALTH_TIMEOUT))
  until run_health; do
    if (( SECONDS >= deadline )); then
      return 1
    fi
    sleep 2
  done
}

swap_and_check() { # binary swap with rollback — used by --update
  chmod +x "$PANEL_DIR/meghify.new"
  if [[ -f "$PANEL_DIR/meghify" ]]; then
    mv -f "$PANEL_DIR/meghify" "$PANEL_DIR/meghify.old"
  fi
  mv -f "$PANEL_DIR/meghify.new" "$PANEL_DIR/meghify"
  if [[ -f /etc/systemd/system/meghify.service ]]; then
    systemctl restart meghify
  else
    systemctl enable --now meghify >/dev/null 2>&1 || systemctl start meghify
  fi
  if wait_healthy; then
    rm -f "$PANEL_DIR/meghify.old"
    return 0
  fi
  if [[ -f "$PANEL_DIR/meghify.old" ]]; then
    warn "the new binary failed the health check — rolling back"
    mv -f "$PANEL_DIR/meghify.old" "$PANEL_DIR/meghify"
    systemctl restart meghify 2>/dev/null || true
  fi
  return 1
}

do_version() {
  if [[ -f "$PANEL_DIR/.env" ]]; then
    local envport
    envport="$(env_get "$PANEL_DIR/.env" APP_PORT)"
    if [[ "$envport" =~ ^[0-9]+$ ]]; then APP_PORT="$envport"; fi
  fi
  if [[ "$STAMP" == "__INSTALLER_STAMP__" ]]; then
    printf 'installer: source (main branch)\n'
  else
    printf 'installer: %s\n' "$STAMP"
  fi
  if [[ -x "$PANEL_DIR/meghify" ]]; then
    printf 'binary:    %s\n' "$("$PANEL_DIR/meghify" --version 2>/dev/null || echo unknown)"
  else
    printf 'binary:    not installed at %s/meghify\n' "$PANEL_DIR"
  fi
  local running
  running="$(api_response_version)"
  if [[ -n "$running" ]]; then
    printf 'running:   %s\n' "$running"
  else
    printf 'running:   not reachable (is the panel up? %s)\n' \
      "meghify status"
  fi
  local lt
  lt="$(gh_real_latest)"
  if [[ -n "$lt" ]]; then
    printf 'release:   %s\n' "$lt"
  else
    printf 'release:   unknown (GitHub API unreachable or no release yet)\n'
  fi
}

do_uninstall() {
  if (( "$(id -u)" != 0 )); then
    die "uninstall needs root — re-run with sudo"
  fi
  if [[ ! -d "$PANEL_DIR" && ! -f /etc/systemd/system/meghify.service \
    && ! -f /usr/local/bin/meghify ]]; then
    die "Meghify is not installed (looked for ${PANEL_DIR} and meghify.service)"
  fi
  if (( ASSUME_YES )); then
    :
  elif [[ -t 0 ]]; then
    local answer
    read -r -p "Remove Meghify and ALL of its data (${PANEL_DIR})? [y/N] " answer
    if [[ "${answer,,}" != y ]]; then
      say "cancelled"
      exit 0
    fi
  else
    die "non-interactive session — re-run with --yes to confirm removal"
  fi
  say "stopping the panel"
  systemctl disable --now meghify >/dev/null 2>&1 || true
  if [[ -f "$PANEL_DIR/repo/docker-compose.yml" ]]; then
    compose_cmd down -v --remove-orphans >/dev/null 2>&1 || true
  fi
  rm -f /etc/systemd/system/meghify.service
  systemctl daemon-reload 2>/dev/null || true
  rm -rf "$PANEL_DIR"
  rm -f /usr/local/bin/meghify
  ok "removed Meghify (the Docker engine, images and other projects stay untouched)"
}

do_update() {
  if (( "$(id -u)" != 0 )); then
    die "update needs root — re-run with sudo"
  fi
  if [[ ! -d "$PANEL_DIR" && ! -f /etc/systemd/system/meghify.service ]]; then
    die "no existing installation — run the installer first"
  fi
  if [[ -f "$PANEL_DIR/.env" ]]; then
    local envport
    envport="$(env_get "$PANEL_DIR/.env" APP_PORT)"
    if [[ "$envport" =~ ^[0-9]+$ ]]; then APP_PORT="$envport"; fi
  fi
  local latest latest_nv current
  latest="$(gh_real_latest)"
  if [[ -z "$latest" ]]; then
    die "could not resolve the latest release (GitHub API limit? try again soon)"
  fi
  latest_nv="${latest#v}"
  current="$(installed_version)"
  current="${current#v}"
  if [[ -n "$current" && "$current" == "$latest_nv" ]]; then
    ok "already on the newest release (${latest})"
    exit 0
  fi
  if [[ -n "$current" ]] && ! ver_ge "$latest_nv" "$current"; then
    die "installed ${current} is newer than release ${latest} — nothing to do"
  fi
  local arch
  arch="$(arch_name)"
  if [[ -z "$arch" ]]; then
    die "unsupported architecture: $(uname -m) (want amd64, arm64 or armv7)"
  fi
  if [[ -f /etc/systemd/system/meghify.service ]]; then
    say "updating to ${latest} (release binary)"
    if ! fetch_release "$latest" "$arch"; then
      die "could not download the release tarball for ${latest} (${arch})"
    fi
    if swap_and_check; then
      ok "updated to ${latest}"
    else
      die "the new release failed the health check — the previous binary is in place"
    fi
  elif [[ -d "$PANEL_DIR/repo" ]]; then
    say "updating to ${latest} (rebuilding the Docker stack from source)"
    rm -rf "$PANEL_DIR/repo"
    local stage src_root
    stage="$(mktemp -d /tmp/meghify-src.XXXXXX)"
    if ! fetch "${REPO_URL}/archive/refs/tags/${latest}.tar.gz" "$stage/src.tar.gz"; then
      rm -rf "$stage"
      die "could not download the ${latest} source archive"
    fi
    if ! tar -xzf "$stage/src.tar.gz" -C "$stage"; then
      rm -rf "$stage"
      die "the ${latest} source archive is corrupted"
    fi
    src_root="$(find "$stage" -mindepth 1 -maxdepth 1 -type d | head -n1)"
    [[ -n "$src_root" ]] || die "the ${latest} source archive is empty"
    mv "$src_root" "$PANEL_DIR/repo"
    rm -rf "$stage"
    say "building and starting the updated stack (a few minutes)"
    if (( VERBOSE )); then
      dc up -d --build || die "compose rebuild failed — see the output above"
    else
      out="$(dc up -d --build 2>&1)" || { printf '%s\n' "$out" | tail -30 >&2; die "compose rebuild failed — re-run with --verbose for the full log"; }
    fi
    if wait_compose_rebuild; then
      ok "updated to ${latest}"
    else
      die "the rebuilt stack did not become healthy in time — check: docker compose -p meghify logs"
    fi
  else
    die "unknown installation layout in ${PANEL_DIR}"
  fi
}

wait_compose_rebuild() {
  say "building and starting the updated stack (a few minutes)"
  local deadline=$((SECONDS + HEALTH_TIMEOUT))
  until run_health; do
    if (( SECONDS >= deadline )); then
      return 1
    fi
    sleep 2
  done
}

# version is answerable without any preflight — keep it scriptable
if [[ "$MODE" == "version" ]]; then
  do_version
  exit 0
fi

print_banner

if [[ "$MODE" == "uninstall" ]]; then
  do_uninstall
  exit 0
fi
if [[ "$MODE" == "update" ]]; then
  do_update
  exit 0
fi

# ---------------------------------------------------------------------------
# install — honor an existing .env port so re-runs print the right link
# ---------------------------------------------------------------------------
if [[ -f "$PANEL_DIR/.env" ]]; then
  envport="$(env_get "$PANEL_DIR/.env" APP_PORT)"
  if [[ "$envport" =~ ^[0-9]+$ ]] && [[ "$envport" != "$APP_PORT" ]]; then
    APP_PORT="$envport"
    say "using the existing APP_PORT=${APP_PORT} from ${PANEL_DIR}/.env"
  fi
fi

# ---------------------------------------------------------------------------
# preflight — check everything first, report once, then decide
# ---------------------------------------------------------------------------
declare -a BROKEN=()

say "Meghify preflight — checking the machine first"

OS="$(uname -s)"; KERN="$(uname -r)"
if [[ "$OS" == Linux ]]; then
  if [[ -r /etc/os-release ]]; then
    # shellcheck disable=SC1091
    DISTRO="$(. /etc/os-release && echo "${PRETTY_NAME:-unknown}")"
  else
    DISTRO="unknown distribution"
  fi
  ok "Linux ${KERN} (${DISTRO})"
else
  fail "Linux is required, found ${OS}"; BROKEN+=("os")
fi

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64)   HOSTARCH="amd64"; ok "amd64 architecture" ;;
  aarch64|arm64)  HOSTARCH="arm64"; ok "arm64 architecture" ;;
  armv7l|armv7)   HOSTARCH="armv7"; ok "armv7 architecture" ;;
  *)              fail "unsupported architecture: ${ARCH} (want amd64, arm64 or armv7)"; BROKEN+=("arch") ;;
esac

if [[ "$(id -u)" -eq 0 ]]; then
  ok "root access"
else
  fail "run as root (sudo curl ... | sudo bash)"; BROKEN+=("root")
fi

if command -v curl >/dev/null 2>&1 || command -v wget >/dev/null 2>&1; then
  ok "download tool present (curl/wget)"
else
  fail "curl or wget required (apt-get install -y curl)"; BROKEN+=("curl")
fi

MEM_MB="$(awk '/MemTotal/ {printf "%d", $2/1024}' /proc/meminfo 2>/dev/null || echo 0)"
if (( MEM_MB >= 700 )); then
  ok "${MEM_MB} MB memory"
elif (( MEM_MB >= 450 )); then
  warn "${MEM_MB} MB memory — works, but 1 GB+ is better for builds"
else
  fail "${MEM_MB} MB memory — 512 MB is the floor"; BROKEN+=("memory")
fi

mkdir -p "$PANEL_DIR" 2>/dev/null || true
DISK_MB="$(df -Pm "$PANEL_DIR" 2>/dev/null | awk 'NR==2 {print $4}')" || true
if [[ -n "$DISK_MB" ]] && (( DISK_MB >= 2048 )); then
  ok "${DISK_MB} MB free disk at ${PANEL_DIR}"
elif [[ -n "$DISK_MB" ]] && (( DISK_MB >= 1024 )); then
  warn "${DISK_MB} MB free disk — 2 GB+ recommended for the build cache"
elif [[ -n "$DISK_MB" ]]; then
  fail "${DISK_MB} MB free disk — 2 GB minimum"; BROKEN+=("disk")
else
  fail "cannot inspect ${PANEL_DIR} — usually missing root permission"; BROKEN+=("disk")
fi

port_busy() {
  if command -v ss >/dev/null 2>&1; then
    ss -ltnH "sport = :${APP_PORT}" 2>/dev/null | grep -q .
  elif command -v netstat >/dev/null 2>&1; then
    netstat -ltn 2>/dev/null | grep -q ":${APP_PORT} "
  else
    return 1
  fi
}

# port_held_by_meghify — a re-run replaces the previous install, so a listener
# that is that install itself (systemd binary or compose container) is not a
# port conflict: the panel necessarily holds its own port while being updated.
port_held_by_meghify() {
  local line pid exe
  if command -v ss >/dev/null 2>&1; then
    line="$(ss -ltnpH "sport = :${APP_PORT}" 2>/dev/null | head -n1)" || line=""
    pid="${line#*pid=}"; pid="${pid%%,*}"; pid="${pid%%)*}"
    if [[ "$pid" =~ ^[0-9]+$ ]]; then
      exe="$(readlink "/proc/${pid}/exe" 2>/dev/null)" || exe=""
      exe="${exe% (deleted)}"
      case "$exe" in
        */meghify|*/meghify.new) return 0 ;;
      esac
    fi
    case "$line" in
      *'"meghify"'*) return 0 ;;
    esac
  elif command -v netstat >/dev/null 2>&1; then
    line="$(netstat -ltnp 2>/dev/null | grep -E "[:.]${APP_PORT}[[:space:]]" | head -n1)" || line=""
    case "$line" in
      *meghify*) return 0 ;;
    esac
  fi
  if command -v docker >/dev/null 2>&1; then
    if docker ps --format '{{.Names}}|{{.Ports}}' 2>/dev/null | grep -E '^meghify' | grep -q ":${APP_PORT}->"; then
      return 0
    fi
  fi
  return 1
}

if port_busy; then
  if port_held_by_meghify; then
    ok "port ${APP_PORT} is held by the running Meghify install — it will be replaced in place"
  else
    fail "port ${APP_PORT} is already in use (set APP_PORT=<port> before installing)"; BROKEN+=("port")
  fi
else
  ok "port ${APP_PORT} is free"
fi
for tport in 80 443; do
  if APP_PORT="$tport" port_busy; then
    warn "port ${tport} is in use — the bundled Traefik may fail to bind it"
  fi
done

if (( FROM_SOURCE )); then
  info "source build requested — checking the host toolchain"
  if command -v git >/dev/null 2>&1; then ok "git present"; else fail "git required for a source build"; BROKEN+=("git"); fi
  if command -v go >/dev/null 2>&1; then
    GOVER="$(go version | sed -E 's/.*go([0-9][0-9.]*)\b.*/\1/')"
    if ver_ge "$GOVER" 1.26; then ok "Go ${GOVER}"; else fail "Go ${GOVER} is too old — module needs 1.26+"; BROKEN+=("go"); fi
  else
    fail "Go 1.26+ required for a source build (https://go.dev/dl/)"; BROKEN+=("go")
  fi
  if command -v node >/dev/null 2>&1; then
    NODEVER="$(node --version | sed 's/^v//')"
    if ver_ge "$NODEVER" 20.0.0; then
      if command -v npm >/dev/null 2>&1; then ok "Node ${NODEVER} + npm"; else fail "Node present but npm missing"; BROKEN+=("npm"); fi
    else
      fail "Node ${NODEVER} is too old — 20+ required"; BROKEN+=("node")
    fi
  else
    fail "Node 20+ required for a source build (https://nodejs.org)"; BROKEN+=("node")
  fi
  info "Docker is optional here — only app deploys need an engine"
else
  info "no toolchain needed — GitHub Actions already compiled the panel binary for you"
  if command -v docker >/dev/null 2>&1; then
    dver="$(docker --version 2>/dev/null | awk '{print $3}' | tr -d ,)"
    if run docker info; then
      DOCKER_STATE="running"
      ok "Docker ${dver} running"
    else
      warn "Docker installed but the daemon is down — trying to start it"
      if run systemctl start docker || run service docker start; then
        sleep 2
        if run docker info; then
          DOCKER_STATE="running"
          ok "Docker ${dver} started"
        else
          DOCKER_STATE="broken"
          warn "Docker daemon will not start (journalctl -u docker) — the panel installs anyway, app deploys wait"
        fi
      else
        DOCKER_STATE="broken"
        warn "could not start the Docker daemon — the panel installs anyway, app deploys wait"
      fi
    fi
  else
    DOCKER_STATE="absent"
    info "Docker missing — the installer will install it next"
  fi
  if [[ "$DOCKER_STATE" == "running" ]]; then
    if docker compose version >/dev/null 2>&1; then
      ok "docker compose plugin present"
    elif command -v docker-compose >/dev/null 2>&1; then
      warn "using legacy docker-compose v1"
    else
      info "compose plugin missing — get.docker.com includes it (docker-compose-plugin)"
    fi
  fi
  if command -v git >/dev/null 2>&1; then ok "git present (source fallback available)"; else info "git missing — not needed, the source archive downloads without it"; fi
fi

if (( ${#BROKEN[@]} > 0 )); then
  echo
  die "preflight found ${#BROKEN[@]} blocking problem(s) — fix them and re-run"
fi

# ---------------------------------------------------------------------------
# install Docker when it is missing (get.docker.com covers the common distros)
# ---------------------------------------------------------------------------
if (( ! FROM_SOURCE )) && [[ "$DOCKER_STATE" != "running" ]]; then
  say "installing Docker (get.docker.com)"
  fetch "https://get.docker.com" /tmp/meghify-docker-install.sh
  if (( VERBOSE )); then
    sh /tmp/meghify-docker-install.sh
  else
    sh /tmp/meghify-docker-install.sh >/tmp/meghify-docker-install.log 2>&1 \
      || { tail -30 /tmp/meghify-docker-install.log >&2; warn "Docker installation failed — see the log above"; DOCKER_STATE="broken"; }
  fi
  rm -f /tmp/meghify-docker-install.sh /tmp/meghify-docker-install.log
  if [[ "$DOCKER_STATE" != "broken" ]]; then
    systemctl enable --now docker >/dev/null 2>&1 || service docker start || true
    sleep 2
    if run docker info; then
      DOCKER_STATE="running"
      ok "Docker installed and running"
    else
      DOCKER_STATE="broken"
      warn "Docker was installed but the daemon is not reachable — the panel installs anyway, app deploys wait"
    fi
  fi
fi

# ---------------------------------------------------------------------------
# fetch the release binary, or fall back to building the source
# ---------------------------------------------------------------------------
mkdir -p "$PANEL_DIR"
RUN_MODE="compose"
SOURCE_OK=0

if (( FROM_SOURCE )); then
  say "cloning the source"
  git clone --depth 1 "${REPO_URL}.git" "$PANEL_DIR/repo"
  say "building the frontend (this can take a minute)"
  if (( VERBOSE )); then (cd "$PANEL_DIR/repo/web" && npm ci && npm run build)
  else (cd "$PANEL_DIR/repo/web" && npm ci --no-audit --no-fund && npm run build) >/dev/null; fi
  say "building the panel binary (linux/${HOSTARCH})"
  if (( VERBOSE )); then (cd "$PANEL_DIR/repo" && go build -tags web_assets -o "$PANEL_DIR/meghify" ./cmd/meghify)
  else (cd "$PANEL_DIR/repo" && go build -tags web_assets -trimpath -o "$PANEL_DIR/meghify" ./cmd/meghify); fi
  RUN_MODE="systemd"
  SOURCE_OK=1
else
  PUSHED_TAG="$(gh_pinned_or_latest_tag)"
  REL_ARCH="$(arch_name)"
  if [[ -n "$PUSHED_TAG" && -n "$REL_ARCH" ]]; then
    say "fetching the release binary (${PUSHED_TAG})"
    if fetch_release "$PUSHED_TAG" "$REL_ARCH"; then
      RUN_MODE="systemd"
      SOURCE_OK=1
    else
      warn "release tarball for ${PUSHED_TAG} (${REL_ARCH}) failed to download — rebuilding from source instead"
    fi
  fi
  if (( ! SOURCE_OK )); then
    if [[ "$DOCKER_STATE" != "running" ]]; then
      die "no release binary is available and Docker is not working — install Docker (or fix the daemon) and re-run, or use --from-source"
    fi
    if ! docker compose version >/dev/null 2>&1 && ! command -v docker-compose >/dev/null 2>&1; then
      die "no release binary is available and docker compose is missing — install the docker-compose-plugin and re-run, or use --from-source"
    fi
    warn "no GitHub release found yet — building the source inside Docker instead"
    say "downloading the Meghify source (${PANEL_VERSION})"
    if [[ "$PANEL_VERSION" == "latest" ]]; then
      ARCHIVE_URL="${REPO_URL}/archive/refs/heads/main.tar.gz"
    else
      ARCHIVE_URL="${REPO_URL}/archive/refs/tags/${PANEL_VERSION}.tar.gz"
    fi
    STAGE="$(mktemp -d /tmp/meghify-src.XXXXXX)"
    if ! fetch "$ARCHIVE_URL" "$STAGE/src.tar.gz" 2>/dev/null; then
      if command -v git >/dev/null 2>&1; then
        warn "archive download failed — falling back to git clone"
        git clone --depth 1 "${REPO_URL}.git" "$PANEL_DIR/repo"
      else
        rm -rf "$STAGE"
        die "could not download ${ARCHIVE_URL} and git is not available"
      fi
    else
      tar -xzf "$STAGE/src.tar.gz" -C "$STAGE"
      SOURCE_ROOT="$(find "$STAGE" -mindepth 1 -maxdepth 1 -type d | head -n1)"
      mv "$SOURCE_ROOT" "$PANEL_DIR/repo"
      rm -rf "$STAGE"
    fi
    RUN_MODE="compose"
  fi
fi

# ---------------------------------------------------------------------------
# configuration — .env is only generated on the first install so the
# AUTH_SECRET (and every stored secret) survives re-runs
# ---------------------------------------------------------------------------
if [[ ! -f "$PANEL_DIR/.env" ]]; then
  say "generating AUTH_SECRET"
  AUTH_SECRET="$(openssl rand -hex 32 2>/dev/null || head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')"
  HOST_IP="$(resolve_host_ip)"
  cat > "$PANEL_DIR/.env" <<EOF
# Generated by the Meghify installer. Losing AUTH_SECRET means every stored
# secret (ssh keys, TOTP seeds) must be re-entered.
AUTH_SECRET=${AUTH_SECRET}
APP_ENV=production
APP_PORT=${APP_PORT}
PANEL_PUBLIC_URL=http://$(url_host "${HOST_IP:-localhost}"):${APP_PORT}
EOF
  # Docker mode maps APP_PORT to the fixed in-container listen port; systemd
  # mode runs the binary on the host and binds APP_PORT directly.
  if [[ "$RUN_MODE" == "compose" ]]; then
    printf 'PANEL_ADDR=:8080\nDATA_DIR=/data\nDOCKER_HOST=unix:///var/run/docker.sock\n' >> "$PANEL_DIR/.env"
  else
    mkdir -p "$PANEL_DIR/data"
    printf 'PANEL_ADDR=:%s\nDATA_DIR=%s/data\n' "$APP_PORT" "$PANEL_DIR" >> "$PANEL_DIR/.env"
  fi
else
  say ".env already exists — keeping your configuration"
fi

# Compose joins the host docker socket group so the distroless nonroot user can
# reach the engine. Add or repair it on installs created before this setting.
if [[ "$RUN_MODE" == "compose" ]]; then
  CURRENT_DOCKER_GID="$(grep '^DOCKER_GID=' "$PANEL_DIR/.env" | head -n1 | cut -d= -f2- || true)"
  if [[ -z "$CURRENT_DOCKER_GID" ]]; then
    DOCKER_GID="$(stat -c '%g' /var/run/docker.sock 2>/dev/null || true)"
    if [[ -z "$DOCKER_GID" ]]; then
      die "could not read the docker socket group from /var/run/docker.sock"
    fi
    if grep -q '^DOCKER_GID=' "$PANEL_DIR/.env"; then
      sed -i "s|^DOCKER_GID=.*|DOCKER_GID=$DOCKER_GID|" "$PANEL_DIR/.env"
    else
      printf 'DOCKER_GID=%s\n' "$DOCKER_GID" >> "$PANEL_DIR/.env"
    fi
  fi
fi

# ---------------------------------------------------------------------------
# start the panel and wait for the API
# ---------------------------------------------------------------------------
if [[ "$RUN_MODE" == "compose" ]]; then
  say "building and starting the stack (a few minutes on the first run)"
  if (( VERBOSE )); then
    dc up -d --build
  else
    out="$(dc up -d --build 2>&1)" || { printf '%s\n' "$out" | tail -30 >&2; die "compose failed — re-run with --verbose for the full log"; }
  fi
else
  say "installing the meghify systemd service"
  cat > /etc/systemd/system/meghify.service <<EOF
[Unit]
Description=Meghify PaaS control plane
After=network-online.target docker.service
Wants=network-online.target

[Service]
WorkingDirectory=${PANEL_DIR}
ExecStart=${PANEL_DIR}/meghify
EnvironmentFile=${PANEL_DIR}/.env
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  # fresh installs have a plain binary; --update style re-runs may ship
  # a meghify.new downloaded before the unit existed
  if [[ -f "$PANEL_DIR/meghify.new" ]]; then
    chmod +x "$PANEL_DIR/meghify.new"
    if [[ -f "$PANEL_DIR/meghify" ]]; then
      mv -f "$PANEL_DIR/meghify" "$PANEL_DIR/meghify.old"
    fi
    mv -f "$PANEL_DIR/meghify.new" "$PANEL_DIR/meghify"
  fi
  systemctl enable meghify >/dev/null 2>&1
  # restart rather than `enable --now`: on a re-run the old process still
  # holds the port and must be replaced by the freshly swapped binary.
  systemctl restart meghify
fi

if wait_healthy; then
  ok "control plane is healthy"
else
  if [[ -f "$PANEL_DIR/meghify.old" ]]; then
    warn "the new binary failed the health check — rolling back"
    mv -f "$PANEL_DIR/meghify.old" "$PANEL_DIR/meghify"
    systemctl restart meghify 2>/dev/null || true
  fi
  die "panel did not become healthy in time — check: journalctl -u meghify / docker compose -p meghify logs"
fi

# ---------------------------------------------------------------------------
# helper CLI — meghify status | logs | restart | version | update | uninstall
# ---------------------------------------------------------------------------
cat > /usr/local/bin/meghify <<'CLI'
#!/usr/bin/env bash
# Meghify management command, installed by scripts/install.sh.
#   meghify status | logs | restart | version | update [--check] | uninstall [--yes]
set -euo pipefail
DIR="{{PANEL_DIR}}"
REPO="https://github.com/tamalmaity-dev/meghify"
cd "$DIR" 2>/dev/null || true

fetch() { # fetch <url> <dest>
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$1" -o "$2"
  else
    wget -qO "$2" "$1"
  fi
}
fetch_txt() { fetch "$1" /dev/stdout 2>/dev/null || true; }
ver_ge() { printf '%s\n%s\n' "$1" "$2" | sort -V | head -n1 | grep -qx "$2"; }
env_get() { sed -n "s/^${2}=//p" "$1" 2>/dev/null | tail -n1 | tr -d '\r'; }

arch_name() {
  case "$(uname -m)" in
    x86_64|amd64)  echo "amd64" ;;
    aarch64|arm64) echo "arm64" ;;
    armv7l|armv7)  echo "armv7" ;;
    *)             echo "" ;;
  esac
}

APP_PORT="$(env_get "$DIR/.env" APP_PORT)"
[[ -n "$APP_PORT" ]] || APP_PORT=8080
url() { printf 'http://127.0.0.1:%s%s\n' "$APP_PORT" "$1"; }
api_get() { fetch_txt "$(url "$1")"; }
health()  { fetch "$(url /api/health)" /dev/null 2>/dev/null; }

latest_tag() {
  sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' \
    <<< "$(fetch_txt https://api.github.com/repos/tamalmaity-dev/meghify/releases/latest)" | head -n1
}
api_version() { # running panel version, empty when not reachable
  sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' \
    <<< "$(api_get /api/version)" | head -n1
}
bin_version() {
  if [[ -x "$DIR/meghify" ]]; then
    "$DIR/meghify" --version 2>/dev/null | awk '{print $2}' || true
  fi
}

composed() { [[ -f "$DIR/repo/docker-compose.yml" ]]; }
serviced() { [[ -f /etc/systemd/system/meghify.service ]]; }
dc() {
  if docker compose version >/dev/null 2>&1; then
    docker compose --project-directory "$DIR" -f "$DIR/repo/docker-compose.yml" -p meghify "$@"
  elif command -v docker-compose >/dev/null 2>&1; then
    docker-compose --project-directory "$DIR" -f "$DIR/repo/docker-compose.yml" -p meghify "$@"
  else
    return 127
  fi
}

swap_and_check() { # swap_and_check <tag> — binary swap with rollback (systemd)
  local tag="$1" stage
  stage="$(mktemp -d /tmp/meghify-rel.XXXXXX)"
  fetch "${REPO}/releases/download/${tag}/meghify-linux-$(arch_name).tar.gz" "$stage/rel.tar.gz"
  tar -xzf "$stage/rel.tar.gz" -C "$stage"
  if [[ ! -f "$stage/meghify" ]]; then
    echo "release archive is malformed (no meghify binary)" >&2
    rm -rf "$stage"
    return 1
  fi
  chmod +x "$stage/meghify"
  if [[ -f "$DIR/meghify" ]]; then
    mv -f "$DIR/meghify" "$DIR/meghify.old"
  fi
  mv -f "$stage/meghify" "$DIR/meghify"
  rm -rf "$stage"
  systemctl restart meghify
  local deadline=$((SECONDS + 90))
  until health; do
    if (( SECONDS >= deadline )); then
      if [[ -f "$DIR/meghify.old" ]]; then
        mv -f "$DIR/meghify.old" "$DIR/meghify"
        systemctl restart meghify 2>/dev/null || true
      fi
      return 1
    fi
    sleep 2
  done
  rm -f "$DIR/meghify.old"
}

cmd="${1:-help}"; shift 2>/dev/null || true
cmd="${cmd#--}"; cmd="${cmd#-}" # accept --status / -status forms too
CHECK=0; YES=0
for a in "$@"; do
  case "$a" in
    --check) CHECK=1 ;;
    --yes|-y) YES=1 ;;
    *) echo "unknown option: $a" >&2; exit 2 ;;
  esac
done

case "$cmd" in
  status)
    if serviced; then systemctl status meghify --no-pager
    elif composed; then dc ps
    else echo "not installed at ${DIR}" >&2; exit 1
    fi ;;
  logs)
    if serviced; then journalctl -u meghify -f -n 200
    elif composed; then dc logs -f --tail 200
    else echo "not installed at ${DIR}" >&2; exit 1
    fi ;;
  restart)
    if serviced; then systemctl restart meghify
    elif composed; then dc restart
    else echo "not installed at ${DIR}" >&2; exit 1
    fi ;;
  version)
    if [[ -x "$DIR/meghify" ]]; then
      echo "binary:  $("$DIR/meghify" --version 2>/dev/null || echo unknown)"
    else
      echo "binary:  not present at ${DIR}/meghify"
    fi
    running="$(api_version)"
    if [[ -n "$running" ]]; then
      echo "running: ${running}"
    else
      echo "running: not reachable (is the panel up? meghify status)"
    fi
    lt="$(latest_tag)"
    if [[ -n "$lt" ]]; then
      echo "release: ${lt}"
    else
      echo "release: unknown (GitHub API unreachable or no release yet)"
    fi ;;
  update)
    lt="$(latest_tag)"
    if [[ -z "$lt" ]]; then
      echo "could not resolve the latest release (GitHub API limit? try again soon)" >&2
      exit 1
    fi
    lt_nv="${lt#v}"
    cur="$(bin_version)"
    cur="${cur:-$(api_version)}"
    cur="${cur#v}"
    if [[ "$CHECK" == 1 ]]; then
      if [[ -n "$cur" && "$cur" == "$lt_nv" ]]; then
        echo "up to date (${lt})"
      elif [[ -n "$cur" ]] && ! ver_ge "$lt_nv" "$cur"; then
        echo "installed ${cur} is newer than release ${lt}"
      else
        echo "an update is available: ${lt} (installed: ${cur:-unknown})"
      fi
      exit 0
    fi
    if serviced; then
      if [[ -n "$cur" && "$cur" == "$lt_nv" ]]; then
        echo "already on the newest release (${lt})"
        exit 0
      fi
      if swap_and_check "$lt"; then
        echo "updated to ${lt}"
      else
        echo "the new release failed the health check — the previous binary is in place" >&2
        exit 1
      fi
    elif composed; then
      echo "==> re-downloading the source (${lt})"
      rm -rf "$DIR/repo"
      STAGE="$(mktemp -d /tmp/meghify-src.XXXXXX)"
      fetch "${REPO}/archive/refs/tags/${lt}.tar.gz" "$STAGE/src.tar.gz"
      tar -xzf "$STAGE/src.tar.gz" -C "$STAGE"
      SRC="$(find "$STAGE" -mindepth 1 -maxdepth 1 -type d | head -n1)"
      mv "$SRC" "$DIR/repo"
      rm -rf "$STAGE"
      echo "==> building and starting the updated stack (a few minutes)"
      dc up -d --build
      deadline=$((SECONDS + 120))
      until health; do
        if (( SECONDS >= deadline )); then
          echo "the rebuilt stack did not become healthy in time (docker compose -p meghify logs)" >&2
          exit 1
        fi
        sleep 2
      done
      echo "updated to ${lt}"
    else
      echo "unknown install layout in ${DIR}" >&2
      exit 1
    fi ;;
  uninstall)
    if [[ -d "$DIR" || -f /etc/systemd/system/meghify.service || -f /usr/local/bin/meghify ]]; then
      :
    else
      echo "not installed" >&2
      exit 0
    fi
    if [[ "$YES" == 1 ]]; then
      :
    elif [[ -t 0 ]]; then
      read -r -p "remove Meghify, its data volumes and ${DIR}? [y/N] " a
      [[ "${a,,}" == y ]] || { echo "cancelled"; exit 0; }
    else
      echo "non-interactive session — re-run with --yes to confirm removal" >&2
      exit 1
    fi
    systemctl disable --now meghify >/dev/null 2>&1 || true
    dc down -v --remove-orphans >/dev/null 2>&1 || true
    rm -f /etc/systemd/system/meghify.service && systemctl daemon-reload 2>/dev/null || true
    rm -rf "$DIR"
    echo "removed." ;;
  *)
    echo "usage: meghify {status|logs|restart|version|update [--check]|uninstall [--yes]}" ;;
esac
CLI
sed -i "s#{{PANEL_DIR}}#${PANEL_DIR}#" /usr/local/bin/meghify
chmod +x /usr/local/bin/meghify

# ---------------------------------------------------------------------------
# final link — resolve the public IP so the printed URL is clickable
# ---------------------------------------------------------------------------
PUBLIC_IP="$(resolve_host_ip)"
if [[ -n "$PUBLIC_IP" ]]; then
  URL="http://$(url_host "$PUBLIC_IP"):${APP_PORT}"
else
  URL="http://<server-ip>:${APP_PORT}"
fi
say "installed. Licensed under ${LICENSE_SPDX}."
echo
dash="$(printf '%*s' 60 '')"; dash="${dash// /─}"
c() { printf "${c_ok}│${c_off} %-58s ${c_ok}│${c_off}\n" "$1"; }
printf "${c_ok}┌%s┐${c_off}\n" "$dash"
c "  Meghify is up and running"
c ""
c "  Your control panel:"
c "     ${URL}"
c ""
c "  Open the link and create the first admin account."
c "  Manage the install: meghify <status|logs|restart|version|update|uninstall>"
printf "${c_ok}└%s┘${c_off}\n" "$dash"
echo
if [[ "$RUN_MODE" == "compose" ]]; then
  info "data:      docker volume meghify-data"
else
  info "data:      ${PANEL_DIR}/data"
fi
info "config:    ${PANEL_DIR}/.env"
info "docs:      ${REPO_URL}"
