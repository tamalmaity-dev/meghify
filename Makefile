.PHONY: help dev build run test vet tidy clean image compose dev-web

# Local development entry point. The web dev server runs on 5173 and proxies
# /api and the SSE stream to the Go panel on 8080.

help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

build: ## Build the panel binary with the embedded SPA
	go build -tags web_assets -trimpath \
		-ldflags "-X github.com/tamalmaity-dev/meghify/internal/version.Version=$(shell git describe --tags --always 2>/dev/null || echo dev)" \
		-o bin/meghify ./cmd/meghify

build-all: ## Cross-compile for linux (amd64, arm64, armv7)
	@for target in linux/amd64 linux/arm64 linux/arm/v7; do \
		os=$${target%/*}; arch=$${target#*/}; \
		echo "building $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -tags web_assets -trimpath \
			-ldflags "-s -w -X github.com/tamalmaity-dev/meghify/internal/version.Version=dev" \
			-o bin/meghify-$$os-$$arch ./cmd/meghify; \
	done

api: ## Run only the Go panel (no embedded frontend)
	go run ./cmd/meghify

dev: ## Run the Go panel alongside the Vite dev server
	@echo "starting panel on :8080 and web on :5173"
	@(go run ./cmd/meghify &) && (cd web && npm run dev)

web: ## Build the frontend into internal/webui/dist
	cd web && npm ci && npm run build

run: build ## Build and run with local dev settings
	@DATA_DIR=.devdata \
	AUTH_SECRET=dev-secret-at-least-16-chars \
	PANEL_ADDR=127.0.0.1:8080 \
	DOCKER_HOST=$${DOCKER_HOST:-unix:///var/run/docker.sock} \
	./bin/meghify

test: ## Run the Go test suite
	go test ./...

vet: ## Vet the Go code
	go vet ./...
	cd web && npm run typecheck

tidy: ## Format and tidy
	gofmt -w ./cmd ./internal
	go mod tidy
	cd web && npm run build

image: ## Build the container image
	docker build -t ghcr.io/tamalmaity-dev/meghify:dev --build-arg VERSION=dev .

compose: ## Run the panel and Traefik as a stack
	docker compose up --build -d
	docker compose logs -f

measure: ## Report the panel's steady-state resource usage
	powershell -NoProfile -ExecutionPolicy Bypass -File scripts/measure.ps1 || \
		echo "see scripts/measure.ps1"

smoke: ## Exercise the API end to end
	node scripts/smoke.mjs

clean: ## Remove build and local data artefacts
	rm -rf bin .devdata internal/webui/dist
	find . -name '*.db' -delete
