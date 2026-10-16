# CI and release pipeline

Meghify ships as **one static binary per platform**, built by GitHub Actions.
The workflows live in `.github/workflows/` and need no extra secrets — they
only use the repository's own `GITHUB_TOKEN`.

---

## ci.yml — "ci" (every push and pull request)

| Job | Steps |
|---|---|
| `backend` | `go vet ./...` → `go test ./...` (Go version read from `go.mod`) |
| `frontend` | `npm ci` (Node 22, npm cache) → `npm run typecheck` → `npm run build` |

This is the gate for `main`; keep it green and the installer's source
fallback also stays trustworthy.

---

## release.yml — "release" (push a tag `v*`)

```bash
git tag v0.1.0
git push origin v0.1.0
```

### Build job (matrix: linux amd64 / arm64 / armv7)

1. checkout
2. Node 22 → `web`: `npm ci` → `npm run build` (Vite output lands in
   `internal/webui/dist`, which the panel embeds)
3. Go (version from `go.mod`) → cross-compile with:
   - `CGO_ENABLED=0` — static, no libc dependencies on the target VPS
   - `-trimpath -ldflags "-s -w -X ...version.Version=<tag without v>
     -X ...version.Commit=<short SHA>"` — the version `meghify --version`
     and `/api/version` report
4. package `meghify-linux-<arch>.tar.gz` containing:
   - `meghify` (the binary, located at the archive root)
   - `docker-compose.yml`
   - `.env.example`
5. upload as a workflow artifact

### Publish job

- stamps `scripts/install.sh` with the commit — it rewrites the
  `__INSTALLER_STAMP__` placeholder, so the uploaded installer reports
  `installer: <sha>` in `--version`
- creates/updates the release with:
  - `meghify-linux-amd64.tar.gz`, `meghify-linux-arm64.tar.gz`,
    `meghify-linux-armv7.tar.gz`
  - `install.sh` (the stamped copy)
  - auto-generated release notes

The installer then resolves the tag through the
`releases/latest` GitHub API and fetches
`meghify-linux-<hostarch>.tar.gz` via
`fetch_release()`.

---

## What that means for users

- one-line install on a VPS = downloading a prebuilt binary; the compile
  fallback is only for the period before the first tag
- `meghify --version` on the server prints the exact tag + commit it runs,
  and `meghify update --check` diffs it against `releases/latest`
- pinning: `PANEL_VERSION=v0.1.0 curl ... | bash` installs a specific
  release and keeps the installer honest about updates

---

## Publishing images

`docker-compose.yml` tags the built image `ghcr.io/tamalmaity-dev/meghify`,
which becomes pushable to GHCR the moment you add a registry job. Until then
the tag exists only locally inside Docker (compose builds it with `--build`
and never pulls).
