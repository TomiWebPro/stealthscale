---
title: "Release automation for Reality-enabled artifacts"
labels: ["chore"]
priority: medium
assignee: ""
created: 2026-08-28T00:00:00Z
---

**Goal:** Tag, build, and publish `stscale` + patched `tailscale` client as a coherent release.

**Context:** `goreleaser` (`.goreleaser.yml`), `flake.nix`, `Dockerfile.*`, and `packaging/deb/*` exist, but the new `xtls/reality` dep (+2-3 MB) and the dual-decoy `config-example.yaml:129` have not been through a full `goreleaser --snapshot` or `nix build`. `LICENSE:44` now includes MPL-2.0 third-party notices that must be in the distributed binary's `LICENSE`.

**Tasks:**
- Run `goreleaser build --snapshot` and `nix build` with the new `go.sum` and verify the `stscale` binary embeds the correct `reality` dest default and `LICENSE` third-party section.
- Build and push `Dockerfile.integration` with the patched `tailscale` image (from `client-patched-tailscale-distribution`).
- Tag `v0.x.0` and publish `CHANGELOG.md` + `LICENSE` + `config-example.yaml` via `gh release` (or `goreleaser release`).

**Acceptance:**
- `gh release view --json` shows the new tag with `stscale` + `tailscale` assets and `LICENSE` containing `xtls/reality` MPL-2.0.
- `make dev` (`fmt`+`lint`+`test`+`build`) passes on `main` after the tag.
