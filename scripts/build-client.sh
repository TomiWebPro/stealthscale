#!/usr/bin/env bash
set -euo pipefail
# Build the single unified binary stscale (server+client, VLESS+Reality).
# There is no separate client — stscale serve and stscale up are the same binary.

OUTPUT="${OUTPUT:-./dist}"

echo "Building single binary stscale (unified server+client, VLESS+Reality)..."
if command -v goreleaser >/dev/null 2>&1; then
  goreleaser build --snapshot --clean
else
  echo "goreleaser not found, falling back to go build for host..."
  mkdir -p "$OUTPUT"
  go build -o "$OUTPUT/stscale" ./cmd/stealthscale
fi

# client/ is reference only (how a tailscale fork would be patched)
# Not built as an artifact — stscale is the client.
if [[ -d "client/example" ]]; then
  echo "Reference: client/example builds as stscale does (not a separate distribution)"
  go build -o "$OUTPUT/stscale" ./cmd/stealthscale 2>/dev/null || true
fi

ls -lh "$OUTPUT/" 2>&1 | tail -20
echo "Done. Single binary stscale is both server (stscale serve) and client (stscale up --vless-uri)."
