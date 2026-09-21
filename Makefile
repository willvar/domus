.PHONY: help build build-win build-prod dev s3 s3-if-local s3-stop s3-reset dev-web clean tidy lint test test-e2e test-dofs test-oss-prefix

BUILD_DIR := dist
VERSION := $(shell cat VERSION 2>/dev/null || echo "dev")
LDFLAGS := -s -w -buildid= -X 'domus/shared/version.Version=$(VERSION)'
DEV_CONFIG ?= config.local.yaml
DEV_RUNTIME_ROOT ?= $(CURDIR)/tmp/dev
DEV_API_BASE ?=
DEV_FRONTEND_HOST ?= 0.0.0.0
DEV_FRONTEND_PORT ?= 8089

help:
	@echo "Usage:"
	@echo "  make build        Build for Linux"
	@echo "  make build-win    Build for Windows"
	@echo "  make build-prod   Build with UPX compression"
	@echo "  make dev          Run Web + dofs serve + worker + Frontend (full local stack)"
	@echo "  make s3           Start the local SeaweedFS development bucket"
	@echo "  make s3-stop      Stop the local SeaweedFS development bucket"
	@echo "  make s3-reset     Stop the local bucket and delete its on-disk data"
	@echo "  make dev-web      Run only Web"
	@echo "  make tidy         Run go mod tidy"
	@echo "  make test         Run all tests"
	@echo "  make test-e2e     Run the controlled real-browser file manager suite"
	@echo "  make test-dofs    Run the real Linux FUSE integration test"
	@echo "  make test-oss-prefix  Verify OSS prefix isolation with disposable SeaweedFS"
	@echo "  make lint         Run golangci-lint + vue-tsc"
	@echo "  make clean        Remove build artifacts"

s3:
	bash scripts/local-s3.sh start

s3-if-local:
	@if grep -qE 'server_endpoint: *"?127\.0\.0\.1:8333' "$(DEV_CONFIG)" 2>/dev/null; then \
		bash scripts/local-s3.sh start; \
	else \
		echo "dev: $(DEV_CONFIG) does not target 127.0.0.1:8333, skipping local S3"; \
	fi

s3-stop:
	bash scripts/local-s3.sh stop

s3-reset:
	bash scripts/local-s3.sh reset

build:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOWORK=off go build -ldflags="$(LDFLAGS)" -trimpath -o $(BUILD_DIR)/domus .
	@echo "Built: $(BUILD_DIR)/domus ($(VERSION))"

build-win:
	@mkdir -p $(BUILD_DIR)
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 GOWORK=off go build -ldflags="$(LDFLAGS)" -trimpath -o $(BUILD_DIR)/domus.exe .
	@echo "Built: $(BUILD_DIR)/domus.exe ($(VERSION))"

build-prod: build
	upx --best $(BUILD_DIR)/domus
	@echo "Compressed: $(BUILD_DIR)/domus"

dev: s3-if-local
	@command -v setsid >/dev/null 2>&1 || { echo "setsid is required for managed development processes" >&2; exit 1; }
	@bash -eu -o pipefail -c '\
		backend_pid=""; dofs_pid=""; worker_pid=""; frontend_pid=""; \
		cleanup() { \
			status="$$?"; \
			trap - EXIT INT TERM; \
			if [[ -n "$$frontend_pid" ]]; then kill -TERM -- "-$$frontend_pid" 2>/dev/null || true; fi; \
			if [[ -n "$$worker_pid" ]]; then kill -TERM -- "-$$worker_pid" 2>/dev/null || true; fi; \
			if [[ -n "$$dofs_pid" ]]; then kill -TERM -- "-$$dofs_pid" 2>/dev/null || true; fi; \
			if [[ -n "$$backend_pid" ]]; then kill -TERM -- "-$$backend_pid" 2>/dev/null || true; fi; \
			for pid in "$$frontend_pid" "$$worker_pid" "$$dofs_pid" "$$backend_pid"; do \
				if [[ -n "$$pid" ]]; then wait "$$pid" 2>/dev/null || true; fi; \
			done; \
			exit "$$status"; \
		}; \
		trap cleanup EXIT; \
		trap "exit 130" INT; \
		trap "exit 143" TERM; \
		setsid go run . dev -c "$(DEV_CONFIG)" --runtime-root "$(DEV_RUNTIME_ROOT)" & \
		backend_pid="$$!"; \
		for _ in $$(seq 1 60); do \
			[[ -f "$(DEV_RUNTIME_ROOT)/config.yaml" ]] && break; \
			sleep 0.5; \
		done; \
		[[ -f "$(DEV_RUNTIME_ROOT)/config.yaml" ]] || { echo "dev: runtime config did not appear" >&2; exit 1; }; \
		setsid go run . dofs serve -c "$(DEV_RUNTIME_ROOT)/config.yaml" & \
		dofs_pid="$$!"; \
		setsid go run . worker -c "$(DEV_RUNTIME_ROOT)/config.yaml" --interval 5 & \
		worker_pid="$$!"; \
		setsid env VITE_API_BASE="$(DEV_API_BASE)" npm --prefix frontend run dev -- --host "$(DEV_FRONTEND_HOST)" --port "$(DEV_FRONTEND_PORT)" --strictPort & \
		frontend_pid="$$!"; \
		wait -n "$$backend_pid" "$$dofs_pid" "$$worker_pid" "$$frontend_pid"\
	'

dev-web:
	go run . start -c "$(DEV_CONFIG)"

test:
	GOWORK=off go test ./... -count=1 -timeout 120s

test-e2e:
	DOMUS_E2E_CONFIG="$(DEV_CONFIG)" \
	DOMUS_E2E_RUNTIME_ROOT="$(DEV_RUNTIME_ROOT)" \
	npm --prefix frontend run test:e2e
	DOMUS_DOFS_LIVE_CONFIG="$(DEV_RUNTIME_ROOT)/config.yaml" \
	go test ./internal/dofsbridge -run '^TestLiveObjectReclamation$$' -count=1 -v

test-dofs:
	$(MAKE) -C ../dofs test-fuse

test-oss-prefix:
	bash scripts/test-oss-prefix.sh

tidy:
	GOWORK=off go mod tidy

lint:
	GOWORK=off golangci-lint run
	cd frontend && npm run check

clean:
	rm -rf $(BUILD_DIR)
