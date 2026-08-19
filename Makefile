.PHONY: help build build-win build-prod dev dev-web clean tidy lint test test-e2e test-dofs

BUILD_DIR := dist
VERSION := $(shell cat VERSION 2>/dev/null || echo "dev")
LDFLAGS := -s -w -buildid= -X 'domus/shared/version.Version=$(VERSION)'
DEV_CONFIG ?= config.yaml
DEV_RUNTIME_ROOT ?= $(CURDIR)/tmp/dev
DEV_API_BASE ?= http://127.0.0.1:8088
DEV_FRONTEND_HOST ?= 127.0.0.1
DEV_FRONTEND_PORT ?= 8089

help:
	@echo "Usage:"
	@echo "  make build        Build for Linux"
	@echo "  make build-win    Build for Windows"
	@echo "  make build-prod   Build with UPX compression"
	@echo "  make dev          Run Web + Frontend"
	@echo "  make dev-web      Run only Web"
	@echo "  make tidy         Run go mod tidy"
	@echo "  make test         Run all tests"
	@echo "  make test-e2e     Run the controlled real-browser file manager suite"
	@echo "  make test-dofs    Run the real Linux FUSE integration test"
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

dev:
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
		setsid go run . dev -c "$(DEV_CONFIG)" --runtime-root "$(DEV_RUNTIME_ROOT)" & \
		backend_pid="$$!"; \
		setsid env VITE_API_BASE="$(DEV_API_BASE)" npm --prefix frontend run dev -- --host "$(DEV_FRONTEND_HOST)" --port "$(DEV_FRONTEND_PORT)" --strictPort & \
		frontend_pid="$$!"; \
		wait -n "$$backend_pid" "$$frontend_pid"\
	'

dev-web:
	go run . start -c "$(DEV_CONFIG)"

test:
	go test ./... -count=1 -timeout 120s

test-e2e:
	DOMUS_E2E_CONFIG="$(DEV_CONFIG)" \
	DOMUS_E2E_RUNTIME_ROOT="$(DEV_RUNTIME_ROOT)" \
	npm --prefix frontend run test:e2e
	DOMUS_DOFS_LIVE_CONFIG="$(DEV_RUNTIME_ROOT)/config.yaml" \
	go test ./internal/dofsbridge -run '^TestLiveObjectReclamation$$' -count=1 -v

test-dofs:
	$(MAKE) -C ../dofs test-fuse

tidy:
	go mod tidy

lint:
	golangci-lint run
	cd frontend && npm run check

clean:
	rm -rf $(BUILD_DIR)
