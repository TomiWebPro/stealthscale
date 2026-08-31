---
title: "DERP fail-closed e2e verification"
labels: ["bug", "stealth", "derp"]
priority: high
assignee: ""
created: 2026-08-28T00:00:00Z
---

**Goal:** Prove DERP is only offered when Reality is satisfied, otherwise fail-closed.

**Context:** `hscontrol/derp/stealth.go:12` (`globalChecker`, `IsStealthSatisfied`) + `hscontrol/stealth/stealth.go:12` (`Checker`/`SetReady`) + `hscontrol/app.go:247` (`gateDERPOnStealth` for `/derp`/`/bootstrap-dns`) implement fail-closed, but no `integration/` or `servertest` test drives the full path with `reality_xtls` and a patched client.

**Tasks:**
- Add `hscontrol/servertest` test that starts `State` with `xray.stealth.enforce:true`, connects a node over `reality_xtls` (via `hscontrol/xray` test dest), and asserts `derp.IsStealthSatisfied` is true for the node's region and `false` for a plain probe.
- Add `integration/` scenario (`integration/README.md`, `cmd/hi/README.md`) with `stealthscale` + 2 patched clients, `derp.server.enabled:true`, `stealth.enforce:true`; verify `MapResponse.DERPMap` is empty when stealth fails and populated when it passes.
- Ensure `derp.server.verify_clients` and `verify_client_ip` do not leak relay topology to external DERP `urls: []` (default).

**Acceptance:**
- `go run ./cmd/hi run TestDERPFailClosed --postgres` passes.
- No DERP fallback is offered to a non-Reality probe (checked via `MapResponse`).
