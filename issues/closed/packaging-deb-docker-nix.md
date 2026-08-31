---
title: "Packaging for Reality (deb, docker, nix)"
labels: ["chore"]
priority: medium
assignee: ""
created: 2026-08-28T00:00:00Z
---

**Goal:** Distributed artifacts must bundle the new Reality deps and dual-decoy defaults.

**Context:** `go.mod:18` now requires `github.com/xtls/reality` (transitively `github.com/cloudflare/circl`, `github.com/juju/ratelimit`, `github.com/pires/go-proxyproto` upgrade `0.9.2`→`0.11.0`), and `hscontrol/xray/reality_client.go:1` needs `golang.org/x/crypto/hkdf`. `flake.nix`/`flake.lock`, `Dockerfile.*`, `nix/module.nix` were only scrubbed for branding (`packaging/deb/*`, `nix/module.nix`) not for Reality.

**Tasks:**
- `nix develop` / `flake.nix` / `flakehashes.json` — ensure `go` 1.26.5 + `reality` + `circl` build.
- `Dockerfile.integration`, `Dockerfile.tailscale-rs`, `Dockerfile.wasmclient` — verify `go mod download` for `xtls/reality`.
- `packaging/deb/*` (`postinst`/`prerm`/`postrm`) and `packaging/systemd/stealthscale.service` — ensure `xray.reality.dest`/`server_names` defaults are in the shipped `config-example.yaml`.
- `nix/module.nix` / `nix/example-configuration.nix` — expose `xray.reality` options.

**Acceptance:**
- `nix build` and `docker build -f Dockerfile.integration` succeed with the new `go.sum:34707`.
- `make build` (`stscale` binary) size delta documented (+2-3 MB for `xtls/reality`).
