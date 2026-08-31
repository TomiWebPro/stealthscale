---
title: "Properly fix VLESS ack / session-ticket tls: unexpected message"
labels: ["bug", "vless", "stealth"]
priority: high
assignee: ""
created: 2026-08-28T00:00:00Z
---

**Goal:** Remove the workaround that skips the VLESS `0x00` ack for `reality_xtls` and fix the root cause.

**Context:** After vendoring `xtls/reality`, `hscontrol/xray/client.go:210` now skips the `io.ReadFull(ack)` for `reality_xtls` because the dest's `NewSessionTicket` (144, `hscontrol/xray/xray_test.go:290` `s2cSaved`) leaves a handshake record that the client's next `Read` sees as `tls: unexpected message` (seen in `go run /tmp/check_raw_simple.go` and `TestServerRealityTLSRoundTrip`). `hscontrol/xray/reality_client.go:120` now uses `SessionTicketsDisabled:false`+`NewLRUClientSessionCache(32)` to try to consume the ticket, but the `reality.Server` replay still leaves the ticket as the next record.

**Tasks:**
- Determine correct `utls.Config` for `RealityUClient` to consume the initial `NewSessionTicket` as part of the handshake (likely `SessionTicketsDisabled:false` + `ClientSessionCache` + handling post-handshake `readSessionTicket`, or `SessionTicketsDisabled:true` with proper `utls` ticket discarding).
- Alternatively, make the dest not send a ticket (or make `reality.Server` not replay it) by setting `reality.Config` `SessionTicketsDisabled`/`NextProtos` correctly, without triggering `payload[0]:4, padding:-144` (`xray_test.go:290`).
- Remove the `if sec == "reality_xtls" { return conn, nil }` bypass in `client.go:210` and make `TestServerRealityTLSRoundTrip` assert the `0x00` ack again.
- Add a regression test that does `DialVLESS` → `WriteVLESSRequest` → `Read` ack → `Write` payload → `ReadAll` on the handler, without `primeRealityLens` hack, against a real `www.cloudflare.com:443` dest (with `DetectPostHandshakeRecordsLens`).

**Acceptance:**
- `hscontrol/xray/client.go:210` no longer has the `reality_xtls` early return; `TestServerRealityTLSRoundTrip` checks the `0x00` ack and still passes.
- `go test ./hscontrol/xray -run TestServerRealityTLSRoundTrip` passes without `primeRealityLens` for the `www.cloudflare.com:443` case (with internet).
