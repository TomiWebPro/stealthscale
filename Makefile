# Build targets
.PHONY: build
build:
	@echo "Building stscale..."
	go build $(PIE_FLAGS) -ldflags "-X main.version=$(VERSION)" -o stscale ./cmd/stealthscale