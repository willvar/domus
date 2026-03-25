.PHONY: help build build-win build-prod dev clean tidy lint

BUILD_DIR := dist
VERSION := $(shell cat VERSION 2>/dev/null || echo "dev")
LDFLAGS := -s -w -buildid= -X 'zephyr/shared/version.Version=$(VERSION)'

help:
	@echo "Usage:"
	@echo "  make build        Build for Linux"
	@echo "  make build-win    Build for Windows"
	@echo "  make build-prod   Build with UPX compression"
	@echo "  make dev          Run in dev mode"
	@echo "  make tidy         Run go mod tidy"
	@echo "  make lint         Run golangci-lint + eslint"
	@echo "  make clean        Remove build artifacts"

build:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -trimpath -o $(BUILD_DIR)/zephyr cmd/zephyr/main.go
	@echo "Built: $(BUILD_DIR)/zephyr ($(VERSION))"

build-win:
	@mkdir -p $(BUILD_DIR)
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -trimpath -o $(BUILD_DIR)/zephyr.exe cmd/zephyr/main.go
	@echo "Built: $(BUILD_DIR)/zephyr.exe ($(VERSION))"

build-prod: build
	upx --best $(BUILD_DIR)/zephyr
	@echo "Compressed: $(BUILD_DIR)/zephyr"

dev:
	go run cmd/zephyr/main.go start

tidy:
	go mod tidy

lint:
	golangci-lint run
	cd frontend && npx eslint .

clean:
	rm -rf $(BUILD_DIR)
