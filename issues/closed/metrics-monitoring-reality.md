---
title: "Metrics and monitoring for Reality transport"
labels: ["feature", "chore"]
priority: low
assignee: ""
created: 2026-08-28T00:00:00Z
---

**Goal:** Operators can observe Reality health and stealth gating.

**Context:** `hscontrol/metrics.go`, `hscontrol/stealth/stealth.go:12` (`Checker`/`SetReady`), and `hscontrol/derp/stealth.go:12` (`IsStealthSatisfied`, `MarkStealthReady`) exist, but no `prometheus` metrics for `reality_handshake_success`/`failure`, `utls_fingerprint` counts, or `derp_gated` events. `config-example.yaml:129` `xray.stealth.probe_interval/timeout` are not yet wired to `Checker`.

**Tasks:**
- Add `prometheus` counters/histograms in `hscontrol/xray/server.go:140` (`reality_handshake_success_total`, `reality_handshake_failure_total` labeled by `dest`/`sni`/`reason`), and in `hscontrol/derp/stealth.go:12` (`derp_gated_total`).
- Wire `xray.stealth.probe_interval`/`probe_timeout` (`hscontrol/types/config.go:697`) to `stealth.Checker` and expose `stealth_ready` gauge.
- Document metrics in `docs/ref/configuration.md` and `README.md`.

**Acceptance:**
- `curl http://<metrics_listen_addr>/metrics` shows the new Reality/DERP metrics.
- `go test ./hscontrol/...` with `stealth.enforce:true` still passes.
