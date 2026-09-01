# StealthScale Makefile — see AGENTS.md Quick Start for usage
VERSION ?= dev
PIE_FLAGS ?=

.PHONY: build test fmt lint generate dev clean

build:
	@echo "Building stscale..."
	go build $(PIE_FLAGS) -ldflags "-X main.version=$(VERSION)" -o stscale ./cmd/stealthscale

test:
	go test -race -short -count=1 ./...

fmt:
	gofumpt -w .
	golines -w --max-len=88 .
	prettier --write "**/*.{md,json,yaml,yml}" || true

lint:
	golangci-lint run --timeout=5m ./...
	buf lint || true

generate:
	go generate ./...
	buf generate || true

dev: fmt lint test build
	@echo "dev: fmt+lint+test+build ok"

clean:
	rm -f stscale
	rm -rf dist/
