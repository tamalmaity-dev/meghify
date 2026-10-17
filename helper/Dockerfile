# The Meghify build helper: everything a deployment needs to run — git, the
# buildpack binaries (nixpacks, railpack) and the docker CLI with buildx and
# compose — so the panel image itself stays distroless and hosts need only
# Docker. Published as ghcr.io/tamalmaity-dev/meghify-helper on every release.
#
# Alpine + musl: both buildpack binaries ship musl static releases, and alpine
# carries docker-cli, buildx and compose as native packages, which keeps the
# image small and multi-arch.
FROM alpine:3.20

ARG NIXPACKS_VERSION=v1.41.0
ARG RAILPACK_VERSION=v0.42.0
ARG TARGETARCH

RUN apk add --no-cache \
      bash \
      ca-certificates \
      curl \
      docker-cli \
      docker-cli-buildx \
      docker-cli-compose \
      git \
      jq \
      openssh-client

# Buildpack binaries. railpack has no armv7 release; nixpacks does, so arm/v7
# images ship nixpacks only.
RUN set -eux; \
    case "$TARGETARCH" in \
      amd64) NIXPKGS_TRIPLE=x86_64-unknown-linux-musl; RAILPACK_TRIPLE=x86_64-unknown-linux-musl ;; \
      arm64) NIXPKGS_TRIPLE=aarch64-unknown-linux-musl; RAILPACK_TRIPLE=arm64-unknown-linux-musl ;; \
      arm)   NIXPKGS_TRIPLE=arm-unknown-linux-musleabihf; RAILPACK_TRIPLE="" ;; \
      *) echo "unsupported architecture: $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    curl -fsSL -o /tmp/nixpacks.tar.gz \
      "https://github.com/railwayapp/nixpacks/releases/download/${NIXPACKS_VERSION}/nixpacks-${NIXPACKS_VERSION}-${NIXPKGS_TRIPLE}.tar.gz"; \
    tar -xzf /tmp/nixpacks.tar.gz -C /usr/local/bin nixpacks; \
    rm -f /tmp/nixpacks.tar.gz; \
    if [ -n "$RAILPACK_TRIPLE" ]; then \
      curl -fsSL -o /tmp/railpack.tar.gz \
        "https://github.com/railwayapp/railpack/releases/download/${RAILPACK_VERSION}/railpack-${RAILPACK_VERSION}-${RAILPACK_TRIPLE}.tar.gz"; \
      tar -xzf /tmp/railpack.tar.gz -C /usr/local/bin railpack; \
      rm -f /tmp/railpack.tar.gz; \
    fi; \
    nixpacks --version; \
    if [ -n "$RAILPACK_TRIPLE" ]; then railpack --version; fi

CMD ["sleep", "infinity"]
