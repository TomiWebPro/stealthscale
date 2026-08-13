# Build targets
.PHONY: build
build: check-deps $(GO_SOURCES) go.mod go.sum
	@echo "Building stscale..."
	go build $(PIE_FLAGS) -ldflags "-X main.version=$(VERSION)" -o stscale ./cmd/stealthscale