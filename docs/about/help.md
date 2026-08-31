# Getting help (StealthScale)

- **Discord**: `https://discord.gg/c84AZQhmpx` — announcements, `#stealthscale-help`, `docker-issues`, `reverse-proxy-issues`, `web-interfaces`
- **GitHub issues**: `https://github.com/tomiwebpro/stealthscale/issues` — report VLESS+Reality, WebUI, DERP stealth-gating, or `xray.secret` bugs with `config.yaml` (redact `xray.secret`/`private_key`), `stscale nodes vless <id>` output (redact `pbk`), and `curl /web/api/health` / `/web/api/derp` + `go test ./hscontrol/stealth -v` output.
- **Docs**: [StealthScale overview](../stealthscale/overview.md), [Install & deploy](../stealthscale/install.md), [Clients](../stealthscale/clients.md), [XRay/VLESS reference](../ref/xray-vless.md), [Threat model](../ref/threat-model.md), [Web UI usage](../usage/webui.md)
- **Health checks**: `curl http://127.0.0.1:8080/health` (control plane), `curl http://127.0.0.1:9090/metrics | grep stealth_ready` (stealth gauge), `curl -H "Authorization: Bearer <api-key>" http://127.0.0.1:8080/web/api/nodes`

> This is the StealthScale help channel — not Headscale. When asking, mention `stscale` version (`stscale version` or `dist/stscale_*`), `xray.security` mode, and `xray.stealth.enforce`/`enforce_control`.
