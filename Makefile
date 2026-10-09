# StealthScale Makefile — see AGENTS.md Quick Start for usage
VERSION ?= dev
PIE_FLAGS ?=
# Project declares go 1.26.7 in go.mod:3. Pin GOTOOLCHAIN so local
# toolchains newer than 1.26.7 don't surface incompatibilities
# (e.g. encoding/json treating `format:` as fatal).
GOTOOLCHAIN ?= go1.26.7

.PHONY: build test fmt lint generate dev clean

build:
	@echo "Building stscale (toolchain $(GOTOOLCHAIN))..."
	GOTOOLCHAIN=$(GOTOOLCHAIN) go build $(PIE_FLAGS) \
		-ldflags "-X main.version=$(VERSION)" -o stscale ./cmd/stealthscale

test:
	GOTOOLCHAIN=$(GOTOOLCHAIN) go test -race -short -count=1 ./...

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
