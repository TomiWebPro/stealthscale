---
title: "Ship patched Tailscale client with VLESS+Reality"
labels: ["feature", "stealth", "vless"]
priority: high
assignee: ""
created: 2026-08-28T00:00:00Z
---

**Goal:** Distribute a patched Tailscale client that dials VLESS+Reality instead of WireGuard, using `hscontrol/xray/reality_client.go:112` (`RealityUClient`) and `hscontrol/xray/client.go:55` (`DialVLESS`).

**Context:** Server now does true `xtls/reality` (`hscontrol/xray/server.go:140`) and `TestServerRealityTLSRoundTrip:358` passes, but stock Tailscale cannot connect. `docs/stealthscale/clients.md` and `docs/client-modification.md` only describe the patch.

**Tasks:**
- Fork `tailscale/tailscale` (pin version matching `go.mod:55` `tailscale.com v1.101.0-pre`), apply patch to replace WireGuard dial with `xray.DialVLESS` + `RealityUClient`.
- Handle `VLESSConfig` (`hscontrol/xray/vless.go:50`) URI parsing (`vless://<uuid>@<host>:<port>?security=reality_xtls&pbk=&sid=&spx=&fp=`) and `NodePort`/`NodeUUID` derivation.
- Build artifacts for `linux/amd64`, `linux/arm64`, `darwin/*`, `windows` and publish under `stealthscale` release (or as `stscale` client binary).
- Add `stealthscale nodes vless <id>` (`cmd/stealthscale/cli/nodes.go:318`) to output `vless://` URI for the patched client.

**Acceptance:**
- `go run ./cmd/hi` with patched client can register and complete a `MapRequest` over `reality_xtls` (no `noise` fallback).
- `docs/stealthscale/clients.md` updated with download links and `stscale up --coordinator --authkey --vless-uri` example.
