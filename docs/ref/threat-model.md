# Threat Model — Reality Deployment

This document audits the stealth properties of StealthScale's `reality_xtls`
transport (`VLESS + Reality` via `github.com/xtls/reality` + `utls`) and what
remains visible to an observer.

## What Reality hides

- **Per-node VLESS listeners** (`xray.listen_addr` + `listen_port`…`max_listen_port`)
  are wrapped with `xtls/reality` (`hscontrol/xray/server.go:140`
  `buildRealityConfig` → `reality.Server`). The TLS handshake is stolen from the
  decoy dest (`xray.reality.dest`, default `www.cloudflare.com:443` with second
  decoy `www.microsoft.com:443`; `server_names` includes both plus bare domains).
  A scanner sees the decoy's real certificate (Cloudflare/Microsoft) via
  `openssl s_client -servername www.cloudflare.com`, not a StealthScale cert.
  `uTLS` (`hscontrol/xray/reality_client.go:112` `RealityUClient`, `hscontrol/xray/client.go:247` `fpToClientHelloID`) shapes the ClientHello (`chrome`, `firefox`, `safari`, `ios`, `randomized`) so JA3/JA4 is a browser, not Go.
- **Noise is inside VLESS**: the Tailscale `controlbase` handshake runs over the
  authenticated VLESS stream (`hscontrol/servertest/xray_vless_test.go`), not on
  the wire.
- **DERP is fail-closed** (`hscontrol/stealth/stealth.go`, `hscontrol/derp/stealth.go`, `hscontrol/app.go:247` `gateDERPOnStealth`): when `xray.stealth.enforce:true` (default) and the Reality transport is not ready, `MapResponse.DERPMap` is empty and `/derp`/`/bootstrap-dns` return `421`. No fingerprintable relay is offered to a non-Reality probe.

## What `enforce_control:false` still exposes

- The control plane's **main `listen_addr`** (`hscontrol/app.go: Serve`) serves
  the Tailscale Noise upgrade at `/ts2021` (`ts2021UpgradePath`) when
  `xray.stealth.enforce_control:false` (old default). That endpoint is
  fingerprintable as Tailscale/Headscale even though data-plane VLESS is stealth.
- **Control-plane TLS** (`tls_letsencrypt_*`, `tls_cert_path`) is still plain
  `crypto/tls`, not Reality. `hscontrol/xray/server.go:140` only covers per-node
  VLESS listeners. To make the control plane indistinguishable from the decoy,
  either (a) wrap `listen_addr` with `reality.Config` (same `Dest`/`ServerNames`/
  `ShortIds`) or (b) place `server_url` behind a Reality-enabled reverse proxy
  (e.g. Xray-core with `reality` inbound). `config-example.yaml:129` documents
  the dual-decoy `reality.dest`/`server_names` for per-node listeners; the same
  values should be used for the control-plane proxy.
- **Security headers** (`hscontrol/app.go:247` `securityHeaders`) are skipped for
  `controlPlanePaths` (`/ts2021`, `/key`, `/derp`, `/bootstrap-dns`, etc.) so
  those responses don't advertise a browser-oriented header set (a fingerprint).
  When `enforce_control:true`, `/ts2021` is not mounted and returns `404`
  without `X-Frame-Options`/`CSP`.
- **Debug** (`hscontrol/debug.go:12`) is loopback+Tailscale only; **OIDC**
  (`hscontrol/oidc.go:12` `register_confirm` cookie) is not a stealth bypass.

Set `xray.stealth.enforce_control:true` (now the default) so nodes must register
over VLESS. Stock Tailscale clients cannot do this; use the StealthScale-patched
client (`hscontrol/xray/client.go:55` `DialVLESS` + `RealityUClient`) or the
unified `stscale up --vless-uri`.

## ShortId / ServerNames enumeration

- `xray.reality.short_ids` defaults to a single entry derived from
  `xray.secret` via `HMAC(secret,"reality-sid")[:4]` (`hscontrol/types/config.go:277` `InitIdentity`). An operator may set `short_ids: ["", "0123456789ab"]` — empty `""` allows an empty ShortId. Only the listed ShortIds pass Reality verification; others are treated as probes and fall back to the decoy.
- `xray.reality.server_names` defaults to `["www.cloudflare.com","www.microsoft.com","cloudflare.com","microsoft.com"]`. Clients must present an SNI in this set (or the dest host) or the Reality handshake fails and the connection is treated as a probe (real decoy cert is presented).
- **Risk**: an active prober who knows a valid `ShortId`/`ServerName` can still probe. `xray.secret` makes `ShortId` unguessable; `ServerNames` should stay as the dual-decoy list, not a custom single domain that is easier to enumerate. Do not publish `xray.secret` or `private_key` (MPL-2.0 `xtls/reality` key).

## xray.secret handling

- `hscontrol/xray/vless.go:152` `NodeUUID`/`NodePort` and `hscontrol/types/config.go:277` `Reality` keypair/`ShortId` are all `HMAC(secret, label)`-derived, so they are not enumerable by an outsider who knows the public namespace and sequential node IDs.
- For `database.type: sqlite`, `xray.secret` is persisted to `filepath.Dir(db.sqlite)/.xray_secret` (`loadOrCreateSecret:332`). For `postgres`, `stateDir==""` and no local file exists, so **`xray.secret` must be set explicitly in `config.yaml`** (enforced in `validateServerConfig:770`; `stscale nodes vless` also errors). Generate with `openssl rand -hex 32`. Rotating the secret changes every node's UUID/port and `pbk`/`sid` — re-issue `vless://` URIs with `stscale nodes vless <id>` after rotation.
- The CLI `stscale nodes vless` uses `types.ResolveXRayIdentity` which mirrors `LoadServerConfig`'s `InitIdentity(stateDir)` so the URI matches the server's listeners.

## Probing and `xray.stealth.probe_interval`

- `xray.stealth.probe_interval`/`probe_timeout` (`hscontrol/types/config.go:697`, default `30s`/`5s`) are currently **reserved for future active probing** of the Reality dest (e.g. periodic `reality.DetectPostHandshakeRecordsLens` health checks). Today `stealth.Checker` is readiness-gated (transport serving → ready, otherwise fail-closed) and `stealth_ready` gauge (`stealthscale_stealth_ready`) is set by `hscontrol/app.go: Serve` after `StartXRayServer` and on shutdown. No active probe is run on the interval yet; the gauge is still useful for alerting (`curl http://<metrics_listen_addr>/metrics | grep stealth_ready`).
- **Active probing risk**: a Reality server that actively dials its dest on every client connection could be detected via timing. The current `DetectPostHandshakeRecordsLens` is called once at `NewServer` (`go reality.DetectPostHandshakeRecordsLens(rc)`) and cached in `reality.GlobalPostHandshakeRecordsLens`; it dials the dest with `utls` once, not per-client. For the self-signed `127.0.0.1:0` test dest, `xray_test.go:290` `primeRealityLens` pre-populates the lens with `[]int{}` to avoid the 5s poll in `reality/tls.go:404`; production dests (`www.cloudflare.com:443` 2044-byte `EncryptedExtensions`) go through the real detection.

## Verification

- `golangci-lint run --new-from-rev=HEAD~1 --timeout=5m` and `prek run --all-files` on `hscontrol/xray`/`hscontrol/types` Reality paths.
- `hscontrol/xray/reality_client.go:66` `VerifyPeerCertificate` uses `reflect`+`unsafe` to read `peerCertificates` from `utls.Conn` (required because `utls` does not expose it). It checks `HMAC-SHA512(pub, authKey) == signature` for the temporary ed25519 cert; fallback is normal PKI verification. Reviewed for Go 1.26.5 `x509.Certificate` layout.

## References

- `hscontrol/xray/server.go:140` `buildRealityConfig`, `reality.Server` handling, `SessionTicketsDisabled:true` (avoids `tls: unexpected message` + `NewSessionTicket` replay).
- `hscontrol/xray/reality_client.go:112` `RealityUClient` (SessionId AEAD, `VerifyPeerCertificate`).
- `hscontrol/xray/client.go:55` `DialVLESS` (`vless://` URI `dest`/`pbk`/`sid`/`spx`/`fp` handling, `NodePort`/`NodeUUID` derivation).
- `config-example.yaml:91` `xray.secret` persistence note, `config-example.yaml:129` `reality.dest` dual decoys.
- `LICENSE:44` third-party MPL-2.0 (`github.com/xtls/reality`) and BSD-3-Clause (`utls`) attributions.
