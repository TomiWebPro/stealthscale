---
title: "Integration e2e: MapRequest over VLESS+Reality"
labels: ["feature", "stealth", "vless"]
priority: high
assignee: ""
created: 2026-08-28T00:00:00Z
---

**Goal:** Prove a full node lifecycle (register, MapRequest, peer visibility) works over `reality_xtls` with the patched client, not just the `hscontrol/xray` unit test.

**Context:** `hscontrol/xray/xray_test.go:358` now does true Reality via `startLocalRealityDest` + `primeRealityLens`, but `hscontrol/servertest/xray_vless_test.go` and `integration/` still drive the old `noise`/`none` path. `cmd/hi` (`cmd/hi/README.md`) is the Docker-based runner for e2e.

**Tasks:**
- Update `hscontrol/servertest/xray_vless_test.go` to use `types.RealityConfig{Dest: "www.cloudflare.com:443", ServerNames: [www.cloudflare.com,www.microsoft.com,cloudflare.com,microsoft.com], ShortIDs}` and `RealityUClient`.
- Add `integration/` scenario `TestVLESSRealityE2E` (2 nodes, `xray.security: reality_xtls`, `xray.reality.dest: www.cloudflare.com:443`, patched `tailscale` image) that does `stscale up` + `tailscale set` and asserts `State.NodeStore` peer visibility and `mapper` MapResponse.
- Run via `go run ./cmd/hi doctor && go run ./cmd/hi run TestVLESSRealityE2E --postgres`.

**Acceptance:**
- `hscontrol/servertest` Reality test passes.
- `integration` Reality e2e passes on both `sqlite` and `postgres` (per `AGENTS.md` DB gotchas).
