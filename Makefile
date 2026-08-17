.PHONY: help build build-win build-prod dev dev-web clean tidy lint test test-e2e test-dofs test-dofs-docker workspace-image workspace-image-local test-workspace-docker

BUILD_DIR := dist
VERSION := $(shell cat VERSION 2>/dev/null || echo "dev")
LDFLAGS := -s -w -buildid= -X 'domus/shared/version.Version=$(VERSION)'
DEV_CONFIG ?= config.yaml
DEV_RUNTIME_ROOT ?= $(CURDIR)/tmp/dev
DEV_API_BASE ?= http://127.0.0.1:8088
DEV_FRONTEND_HOST ?= 127.0.0.1
DEV_FRONTEND_PORT ?= 8089
WORKSPACE_IMAGE ?= domus-workspace:0.1.0

help:
	@echo "Usage:"
	@echo "  make build        Build for Linux"
	@echo "  make build-win    Build for Windows"
	@echo "  make build-prod   Build with UPX compression"
	@echo "  make dev          Run DOFS + Workspace Manager + Web + Frontend"
	@echo "  make dev-web      Run only Web against an existing Workspace Manager"
	@echo "  make tidy         Run go mod tidy"
	@echo "  make test         Run all tests"
	@echo "  make test-e2e     Run the real browser upload flow"
	@echo "  make test-dofs    Run the real Linux FUSE integration test"
	@echo "  make test-dofs-docker  Run the opt-in FUSE -> Docker bind test"
	@echo "  make workspace-image  Build the curated user workspace image"
	@echo "  make test-workspace-docker  Run the opt-in real Docker workspace test"
	@echo "  make lint         Run golangci-lint + vue-tsc"
	@echo "  make clean        Remove build artifacts"

build:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -trimpath -o $(BUILD_DIR)/domus .
	@echo "Built: $(BUILD_DIR)/domus ($(VERSION))"

build-win:
	@mkdir -p $(BUILD_DIR)
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -trimpath -o $(BUILD_DIR)/domus.exe .
	@echo "Built: $(BUILD_DIR)/domus.exe ($(VERSION))"

build-prod: build
	upx --best $(BUILD_DIR)/domus
	@echo "Compressed: $(BUILD_DIR)/domus"

dev: workspace-image-local
	@command -v setsid >/dev/null 2>&1 || { echo "setsid is required for managed development processes" >&2; exit 1; }
	@bash -eu -o pipefail -c '\
		backend_pid=""; frontend_pid=""; \
		cleanup() { \
			status="$$?"; \
			trap - EXIT INT TERM; \
			if [[ -n "$$frontend_pid" ]]; then kill -TERM -- "-$$frontend_pid" 2>/dev/null || true; fi; \
			if [[ -n "$$backend_pid" ]]; then kill -TERM -- "-$$backend_pid" 2>/dev/null || true; fi; \
			if [[ -n "$$frontend_pid" ]]; then wait "$$frontend_pid" 2>/dev/null || true; fi; \
			if [[ -n "$$backend_pid" ]]; then wait "$$backend_pid" 2>/dev/null || true; fi; \
			exit "$$status"; \
		}; \
		trap cleanup EXIT; \
		trap "exit 130" INT; \
		trap "exit 143" TERM; \
		setsid go run . dev -c "$(DEV_CONFIG)" --runtime-root "$(DEV_RUNTIME_ROOT)" --image "$(WORKSPACE_IMAGE)" & \
		backend_pid="$$!"; \
		setsid env VITE_API_BASE="$(DEV_API_BASE)" npm --prefix frontend run dev -- --host "$(DEV_FRONTEND_HOST)" --port "$(DEV_FRONTEND_PORT)" --strictPort & \
		frontend_pid="$$!"; \
		wait -n "$$backend_pid" "$$frontend_pid"\
	'

dev-web:
	go run . start -c "$(DEV_CONFIG)"

test:
	go test ./... -count=1 -timeout 120s

test-e2e: workspace-image-local
	DOMUS_E2E_CONFIG="$(DEV_CONFIG)" \
	DOMUS_E2E_RUNTIME_ROOT="$(DEV_RUNTIME_ROOT)" \
	DOMUS_E2E_WORKSPACE_IMAGE="$(WORKSPACE_IMAGE)" \
	npm --prefix frontend run test:e2e

test-dofs:
	DOFS_FUSE_INTEGRATION=1 go test ./internal/dofs -run 'Test(FUSEMount|ManagerOwnsRealFUSE)' -v -count=1

test-dofs-docker: workspace-image-local
	DOFS_DOCKER_INTEGRATION=1 DOFS_DOCKER_IMAGE="$(WORKSPACE_IMAGE)" go test ./internal/dofs -run TestFUSEBindMountIntoDocker -v -count=1

workspace-image:
	docker build --pull -t "$(WORKSPACE_IMAGE)" deploy/workspace

workspace-image-local:
	docker build -t "$(WORKSPACE_IMAGE)" deploy/workspace

test-workspace-docker:
	DOMUS_WORKSPACE_DOCKER_INTEGRATION=1 DOMUS_WORKSPACE_TEST_IMAGE="$(WORKSPACE_IMAGE)" go test ./internal/workspace -run TestDockerRuntimeIntegration -v -count=1

tidy:
	go mod tidy

lint:
	golangci-lint run
	cd frontend && npm run check

clean:
	rm -rf $(BUILD_DIR)
