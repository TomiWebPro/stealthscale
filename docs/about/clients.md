# Client and operating system support (StealthScale)

StealthScale's **unified binary** (`stscale`) is both coordinator and node — it is the stealth-capable client. There is no separate patched `tailscaled` required, though a patch reference exists.

## Unified binary — recommended (VLESS+Reality)

Build or download the single `stscale` binary (`stscale serve` and `stscale up` are the same binary):

```bash
make build                      # ./stscale
# or download: ghcr.io/tomiwebpro/stealthscale or github.com/tomiwebpro/stealthscale/releases
curl -LO https://github.com/TomiWebPro/stealthscale/releases/latest/download/stscale_linux_amd64
```

Supported targets via `.goreleaser.yml` (CGO_ENABLED=0): `linux/amd64`, `linux/arm64`, `linux/arm` (generic), `linux/arm_6` (Pi Zero GOARM=6), `linux/arm_7` (Pi 2/3), `darwin/amd64`, `darwin/arm64`, `freebsd/amd64`, `freebsd/arm64`, `windows/amd64`, `windows/arm64`. On any device:

```shell
stscale up \
  --coordinator https://ctl.example.com \
  --authkey <pre-auth-key> \
  --vless-uri 'vless://<uuid>@<addr>:<port>?security=reality_xtls&pbk=<pubkey>&sid=<sid>&dest=www.cloudflare.com%3A443&fp=chrome'
# fetch the URI: stscale nodes vless <node-id> on the coordinator
```

This dials VLESS+Reality via `hscontrol/xray/client.go` (`DialVLESS` + `RealityUClient` with uTLS) and speaks `noise` (TS2021) inside the authenticated stream. See [StealthScale clients](../stealthscale/clients.md) and [XRay/VLESS reference](../ref/xray-vless.md).

| OS | Supports unified `stscale` | Notes |
|---|---|---|
| Linux | Yes | Primary target; embeds VLESS+Reality |
| macOS / Windows | Yes | Same binary via goreleaser |
| FreeBSD / OpenBSD | Yes | Built from source |
| Android / iOS / tvOS / other Tailscale official clients | Requires patch | Official Tailscale clients dial WireGuard, not VLESS. With default `xray.stealth.enforce_control:true` they **cannot** register (no `/ts2021`). Use `stscale` or patch Tailscale to import `hscontrol/xray.DialVLESS` — see [Client modification guide](../client-modification.md) and `client/patch/direct.go.diff`. Only set `enforce_control:false` if you need stock-client compat (then fingerprintable). |

## Stock Tailscale clients — not compatible by default

We aim to support the last 10 releases of the Tailscale client for control-plane features, but the **data plane is StealthScale-only**. When `enforce_control:true` (default), the control plane's `/ts2021` noise endpoint is not mounted — only VLESS stealth is offered. The legacy connect pages (`usage/connect/*`) were removed for this reason; see [StealthScale overview](../stealthscale/overview.md#bootstrap-discovery-and-steady-state-transport) → discovery may be non-stealth, but all post-discovery transport is VLESS stealth.

## Instructions endpoint

A legacy Tailscale connect endpoint is still available at `/apple` and `/windows` on a running instance for reference, but it describes the **non-stealth** path and remains `200` even when `enforce_control:true` — only `/ts2021` is gated (`hscontrol/app.go:509-516`), fingerprinting risk noted in `hscontrol/app.go:530`. Prefer the unified binary.

## Reference patch (optional)

If you prefer `tailscaled` with the VLESS dial patch, see `client/README.md` and `client/patch/direct.go.diff` + `client/example/main.go`. The patch replaces `control/controlclient` dial with `xray.DialVLESS` and adds `--vless-uri` / `TS_VLESS_URI`. The transport (`VLESS + noise + HTTP/2`) is identical.

For help see [FAQ](./faq.md) and [StealthScale clients](../stealthscale/clients.md).
