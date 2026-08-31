---
title: "Security audit and threat model for Reality deployment"
labels: ["chore", "stealth"]
priority: medium
assignee: ""
created: 2026-08-28T00:00:00Z
---

**Goal:** Document and audit the stealth properties before launch.

**Context:** Previous subagents flagged: HMAC-keyed `NodeUUID`/`NodePort` (`hscontrol/xray/vless.go:152`), self-signed Reality simulation removed, but product still has: `hscontrol/app.go:247` `controlPlanePaths` bypass, `hscontrol/debug.go:12` loopback+Tailscale only, `hscontrol/oidc.go:12` cookie `register_confirm`, and the `reality` dest `www.cloudflare.com:443` vs `www.microsoft.com:443` choice matters for the 8192 bug. `LICENSE:44` now attributes MPL-2.0.

**Tasks:**
- Write a short `docs/ref/threat-model.md` (or `docs/stealthscale/overview.md` appendix) covering: (a) what Reality hides vs what `enforce_control:false` still exposes, (b) `ShortId`/`ServerNames` enumeration risk, (c) `xray.secret` handling, (d) `xray.stealth.probe` vs active probing.
- Run `golangci-lint run --new-from-rev=HEAD~1` and `prek run --all-files` (per `AGENTS.md`) on the Reality code paths (`hscontrol/xray/server.go:140`, `reality_client.go:112`, `types/config.go:277`).
- Review `hscontrol/xray/reality_client.go:66` `VerifyPeerCertificate` (unsafe `peerCertificates` reflect) for correctness with Go 1.26.5.

**Acceptance:**
- `docs/ref/threat-model.md` (or equivalent) is merged and linked from `README.md`.
- No `golangci-lint` high-severity findings for `xray`/`types` Reality code.
