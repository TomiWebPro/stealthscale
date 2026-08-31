---
title: "Enforce control-plane stealth (gate /ts2021, Reality for control TLS)"
labels: ["feature", "stealth"]
priority: high
assignee: ""
created: 2026-08-28T00:00:00Z
---

**Goal:** Make the control plane indistinguishable from decoy TLS; remove plaintext Noise fingerprint.

**Context:** `hscontrol/app.go:247` gates `/ts2021` only when `xray.stealth.enforce_control:true` (default `false` in `hscontrol/types/config.go:697`), so by default the Noise endpoint is still exposed. Control-plane TLS (`tls_letsencrypt_*`, `tls_cert_path`) is still plain `crypto/tls`, not `xtls/reality` (`hscontrol/xray/server.go:140` only covers per-node VLESS listeners).

**Tasks:**
- Change `xray.stealth.enforce_control` default to `true` (or at least document as required for stealth) and ensure `hscontrol/app.go:247` + `securityHeaders` correctly skips only control paths after gating.
- Either (a) wrap the main `listen_addr` (`hscontrol/app.go: Serve`) with `reality.Config` (Dest=`xray.reality.dest`, ServerNames=`xray.reality.server_names`, ShortIds) or (b) document that `server_url` must be behind a Reality-enabled reverse proxy and provide an example.
- Update `config-example.yaml:129` (`reality.dest` default `www.cloudflare.com:443`, `server_names` dual decoys) to cover control-plane when `enforce_control:true`.

**Acceptance:**
- With `enforce_control:true`, `curl https://<server_url>/ts2021` does not expose Noise; `nmap`/`ja3` sees only decoy cert (Cloudflare/Microsoft) via `openssl s_client -servername`.
- `go test ./hscontrol/...` with `enforce_control:true` still passes registration via VLESS.
