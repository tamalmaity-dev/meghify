# syntax=docker/dockerfile:1.7

# A single-stage build that produces one small, static, distroless image. The
# compiled React SPA is embedded into the Go binary, so there is nothing to copy
# from a builder and the final image contains only the executable and its data.
#
# CGO is disabled so the binary is fully static and the result runs on scratch.

ARG GO_VERSION=1.26
ARG NODE_VERSION=22-alpine

# --- frontend ---
FROM node:${NODE_VERSION} AS web
WORKDIR /web
COPY web/package.json web/package-lock.json* ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
# The Vite config writes straight into internal/webui/dist so the Go build can
# pick it up with a single //go:embed.
RUN npm run build

# --- backend ---
FROM golang:${GO_VERSION} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# The web build writes to ../internal/webui/dist, which resolves to
# /internal/webui/dist inside the web stage's /web working directory.
COPY --from=web /internal/webui/dist ./internal/webui/dist
# linux/arm64 works too, which is what makes the panel usable on a Pi.
ENV CGO_ENABLED=0 GOOS=linux
# Declared so Compose can stamp the release metadata into the binary; the shell
# fallbacks keep plain `docker build` working without arguments.
ARG VERSION=dev
ARG COMMIT=unknown
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -tags web_assets -trimpath -ldflags="-s -w \
    -X github.com/tamalmaity-dev/meghify/internal/version.Version=${VERSION:-dev} \
    -X github.com/tamalmaity-dev/meghify/internal/version.Commit=${COMMIT:-unknown}" \
    -o /out/meghify ./cmd/meghify && \
    mkdir -p /out/data

# --- runtime ---
FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=build /out/meghify /usr/local/bin/meghify
# The distroless image runs as uid/gid 65532. A freshly created named volume is
# root-owned, so /data must ship with the right ownership or SQLite cannot even
# create its first file.
COPY --from=build --chown=65532:65532 /out/data /data

# CA certificates so the panel can reach Let's Encrypt and git providers.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

LABEL org.opencontainers.image.title="Meghify" \
      org.opencontainers.image.description="A lightweight self-hosted PaaS" \
      org.opencontainers.image.licenses="Apache-2.0"

VOLUME ["/data"]
EXPOSE 8080 6001

# All configuration is read from the environment, so no config file is needed.
ENTRYPOINT ["/usr/local/bin/meghify"]
