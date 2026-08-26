# Install & Deploy (StealthScale, VLESS+Reality_XTLS)

StealthScale is a single binary `stscale` (from `make build`) that is **both server and client** — same `hscontrol/xray` transport, no code difference.

## Build

```bash
# Nix dev shell (Go 1.26.1, buf, golangci-lint, prek)
nix develop
prek install

# Full dev: fmt + lint + test + build
make dev
make build          # => ./stscale
make test           # go test ./...
make fmt
make lint
```

Requires Go 1.26.1+.

## Configure

```bash
cp config-example.yaml /etc/stealthscale/config.yaml
# edit server_url, keep stealth defaults:
# xray:
#   enabled: true
#   security: reality_xtls
#   utls_fingerprint: chrome
#   reality:
#     dest: "" # auto from server_url, or www.microsoft.com:443
#   stealth:
#     enforce: true
```

`config-example.yaml` now defaults to `reality_xtls` — VLESS+Reality via XTLS+uTLS. No cert needed for dest-based Reality. For `tls`/`xtls`, set `cert_file`/`key_file`.

DERP fallback is **gated by stealth**: `xray.stealth.enforce:true` means if Reality not satisfied, DERPMap is empty (fail-closed). See `docs/ref/xray-vless.md`.

## Run Server

```bash
stscale serve --config /etc/stealthscale/config.yaml
curl -s http://127.0.0.1:8080/health | jq
curl -s http://127.0.0.1:8080/web | head   # WebUI embedded
```

Systemd:
```ini
[Unit]
Description=StealthScale (VLESS+Reality)
After=network.target
[Service]
ExecStart=/usr/local/bin/stscale serve --config /etc/stealthscale/config.yaml
Restart=always
[Install]
WantedBy=multi-user.target
```

## Create User & PreAuth Key

```bash
stscale users create alice
stscale preauthkeys create --user <id> --reusable --expiration 24h
# or: stscale apikeys create --expiration 90d   # for WebUI/API
```

## Connect Patched Client (Unified Codebase)

The patched client **reuses same `hscontrol/xray` package** — no separate repo. Build once, use as client dialer:

```bash
# Get VLESS URI for node 1
stscale nodes vless 1
# => vless://<uuid>@<addr>:<port>?security=reality_xtls&fp=chrome&type=tcp&flow=xtls-rprx-vision

# On client host (patched tailscaled uses same xray dialer)
tailscale up \
  --login-server https://ctl.example.com \
  --authkey <preauthkey> \
  --vless-uri 'vless://<uuid>@<addr>:<port>?security=reality_xtls&fp=chrome&type=tcp&flow=xtls-rprx-vision'

# Or test via stscale client
stscale client --vless-uri 'vless://...' --login-server https://ctl.example.com
```

See `docs/client-modification.md` for patching stock tailscale to import `hscontrol/xray.DialVLESS`.

## WebUI

Visit `http://<server>:8080/web` or `/admin` — same for server and client. See `docs/usage/webui.md`.

## Verify Stealth

```bash
go test ./hscontrol/xray -run TestReality -v
go test ./hscontrol/stealth -v
curl -s http://127.0.0.1:8080/web/api/derp | jq .stealth_satisfied
curl -s http://127.0.0.1:8080/web/api/health | jq
```

If `stealth_satisfied:false`, DERP regions will be empty (fail-closed), proving gating.

## Update Checks

Scheduler daily pushes code once per day; to update server:

```bash
git pull # daily-push already did at 02:00 UTC
make build && sudo systemctl restart stealthscale
```
