---
title: "Production reality dest lens handling (no primeRealityLens hack)"
labels: ["bug", "stealth"]
priority: medium
assignee: ""
created: 2026-08-28T00:00:00Z
---

**Goal:** In production (`www.cloudflare.com:443`/`www.microsoft.com:443`) the server must not rely on `primeRealityLens` (test-only `GlobalPostHandshakeRecordsLens` pre-population).

**Context:** `hscontrol/xray/server.go:140` `buildRealityConfig` does `go reality.DetectPostHandshakeRecordsLens(rc)` which dials the dest with `utls` (`record_detect.go:21`) to learn `PostHandshakeRecordsLens`/`MaxCSSMsgCount`. For the unit test's self-signed `127.0.0.1:0` dest this dial fails cert verification, so `hscontrol/xray/xray_test.go:290` `primeRealityLens` pre-populates `reality.GlobalPostHandshakeRecordsLens` with `[]int{}` to avoid the 5s poll in `tls.go:404`. Production dests (Cloudflare/Microsoft) have 2044-byte `EncryptedExtensions` (vs 32 for local) that hit the `>512` branch in `tls.go:341`.

**Tasks:**
- Ensure `DetectPostHandshakeRecordsLens` is called once at `NewServer` and its result is awaited (or at least not blocking the first client `reality.Server` for 5s). Consider calling it synchronously on startup with a timeout or caching the lens to disk.
- Verify with a real dest (`go run /tmp/check_real_dest.go` with `www.cloudflare.com:443`) that `hs.handshake()` no longer yields `payload[0]:4, padding:-144` and `Encrypted Extensions: 2044` is handled.
- Remove or gate `primeRealityLens` so it is only used when `Dest` is `127.0.0.1`.

**Acceptance:**
- `TestServerRealityTLSRoundTrip` can run against `www.cloudflare.com:443` (with internet) without `primeRealityLens` and still pass.
- `reality.DetectPostHandshakeRecordsLens` completes within `xray.stealth.probe_timeout` (5s) for both decoys.
