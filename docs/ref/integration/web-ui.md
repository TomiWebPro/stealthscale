# Web interface for StealthScale

StealthScale ships an **embedded Web UI** built into the `stscale` binary itself — no separate service.

- Served at `http://<server>:8080/web` and `http://<server>:8080/admin` (alias), same handler via `hscontrol/webui` (`Register(mux, cfg, state)` in `hscontrol/app.go:createRouter`).
- Talks to the live control plane (`state.State`). Tabs: Nodes, Users, PreAuthKeys, Policy (HuJSON), DERP + `stealth_satisfied` / `shouldIncludeDERP`, VLESS per-node `vless://` URI, Health.
- Hardened by default: when `xray.stealth.enforce:true` and `xray.stealth.enforce_control:true` (the defaults), every `/web` and `/web/api/*` request requires `Authorization: Bearer <api-key>` or `X-API-Key`, otherwise `401`. See [Web UI usage](../../usage/webui.md) and [XRay/VLESS reference](../xray-vless.md).
- Dark theme matches the stealth console (`--bg:#0b0e14`).

The full API is documented at [Web UI usage](../../usage/webui.md).

## Community frontends (optional)

The following third-party frontends were built for Headscale and may still be used with StealthScale's HTTP API (`/api/v1`) as an alternative to the embedded UI. They are **not** maintained by the StealthScale authors.

- [stscale-ui](https://github.com/gurucomputing/stscale-ui) — web frontend for the Tailscale-compatible coordination server
- [StealthScaleUi](https://github.com/simcu/stscale-ui) — static admin UI, no backend required
- [Headplane](https://github.com/tale/headplane) — advanced Tailscale-inspired frontend
- [stscale-admin](https://github.com/GoodiesHQ/stscale-admin)
- [ouroboros](https://github.com/yellowsink/ouroboros)
- [unraid-stscale-admin](https://github.com/ich777/unraid-stscale-admin)
- [stscale-console](https://github.com/rickli-cloud/stscale-console)
- [stscale-piying](https://github.com/wszgrcy/stscale-piying)
- [HeadControl](https://github.com/ahmadzip/HeadControl)
- [StealthScale Manager](https://github.com/hkdone/headscalemanager) — Android
- [StealthScale UI](https://github.com/MunMunMiao/stscale-ui)
- [StealthScale Panel](https://github.com/stscale-panel/panel)

For support, use the embedded UI (`/web`) or ask in the StealthScale Discord `web-interfaces` channel. For reverse-proxy and TLS in front of the embedded UI, see [Reverse proxy](./reverse-proxy.md) and [TLS](../tls.md).
