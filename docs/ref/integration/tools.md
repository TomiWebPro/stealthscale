# Tools related to StealthScale

!!! warning "Community contributions — verify StealthScale compat"

    This page contains community contributions originally for Headscale. Most tools work with StealthScale's HTTP API (`/api/v1`, `/api/v2`) and `hscontrol/db` schema, but they may assume WireGuard or Headscale defaults (`derp.urls` pointing at Tailscale Inc, no VLESS). Verify `xray.*` / `stealth.*` handling and `ghcr.io/tomiwebpro/stealthscale` image before use. Not maintained by StealthScale authors.

This page collects third-party tools, client libraries, and scripts related to StealthScale. For StealthScale's embedded Web UI see [Web UI](../../usage/webui.md) and [Integration Web UI](./web-ui.md).

- [stscale-operator](https://github.com/infradohq/stscale-operator) - StealthScale Kubernetes Operator
- [tailscale-manager](https://github.com/singlestore-labs/tailscale-manager) - Dynamically manage Tailscale route
  advertisements
- [headscalebacktosqlite](https://github.com/bigbozza/headscalebacktosqlite) - Migrate Headscale/StealthScale from PostgreSQL back to SQLite (Headscale-named, works with StealthScale's `db.sqlite` schema)
- [stscale-pf](https://github.com/YouSysAdmin/stscale-pf) - Populates user groups based on user groups in Jumpcloud
  or Authentik
- [stscale-client-go](https://github.com/hibare/stscale-client-go) - A Go client implementation for the StealthScale
  HTTP API.
- [stscale-zabbix](https://github.com/dblanque/stscale-zabbix) - A Zabbix Monitoring Template for the StealthScale
  Service.
- [tailscale-exporter](https://github.com/adinhodovic/tailscale-exporter) - A Prometheus exporter for StealthScale that
  provides network-level metrics using the StealthScale API.
