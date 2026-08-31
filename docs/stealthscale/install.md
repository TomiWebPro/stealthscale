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
#   secret: "" # auto to .xray_secret for sqlite; MUST set for postgres (openssl rand -hex 32)
#   utls_fingerprint: chrome
#   reality:
#     dest: "www.cloudflare.com:443" # dual decoys: www.cloudflare.com/www.microsoft.com + bare
#     server_names:
#       - www.cloudflare.com
#       - www.microsoft.com
#       - cloudflare.com
#       - microsoft.com
#     short_ids: []  # auto from secret; may contain "" for empty shortId
#     spider_x: "/"
#   stealth:
#     enforce: true
#     enforce_control: true # hide /ts2021 Noise when true (requires VLESS client)
```

`config-example.yaml` now defaults to `reality_xtls` — VLESS+Reality via `github.com/xtls/reality` (MPL-2.0) + uTLS. No cert needed for dest-based Reality (steals `www.cloudflare.com:443` by default; `www.microsoft.com:443` also works but its 8273-byte cert exceeds the 8192 pre-read limit in `reality`). For `tls`/`xtls`, set `cert_file`/`key_file`. `xray.secret` is auto-persisted to `.xray_secret` next to `db.sqlite` for sqlite; for postgres it **must** be set explicitly or the server will fail to start and `stscale nodes vless` URIs will be unstable.

DERP fallback is **gated by stealth**: `xray.stealth.enforce:true` means if Reality not satisfied, DERPMap is empty (fail-closed). See `docs/ref/xray-vless.md`.

## TLS (optional)

The WebUI, control plane, and VLESS endpoints are served by the same listener. You have two ways to enable TLS:

**Bring your own certificate** — set `tls_cert_path` and `tls_key_path` (the cert must contain the full chain):

```yaml
tls_cert_path: /etc/stealthscale/tls.crt
tls_key_path: /etc/stealthscale/tls.key
```

**Let's Encrypt / ACME** — set `tls_letsencrypt_hostname` to the name that resolves to `server_url`. The certificate and account are cached in `tls_letsencrypt_cache_dir`:

```yaml
tls_letsencrypt_hostname: ctl.example.com
tls_letsencrypt_listen: ":http"          # HTTP-01 (default) or ":443" for TLS-ALPN-01
tls_letsencrypt_cache_dir: /var/lib/stealthscale/.cache
tls_letsencrypt_challenge_type: HTTP-01  # or TLS-ALPN-01
```

- `HTTP-01` requires stscale to be reachable on port 80 (default `tls_letsencrypt_listen: ":http"`).
- `TLS-ALPN-01` validates on the `listen_addr` port (usually 443) — forward port 443 to it if needed.

If neither is set, stscale serves plaintext HTTP (fine behind a reverse proxy that terminates TLS). Full reference: `docs/ref/tls.md`.

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

!!! note "A stealth-capable client is required for the data plane"

    The `stscale` binary (or a Tailscale patched with `hscontrol/xray`) is the
    stealth-capable client. During `stscale up` it performs a stealth transport
    check that validates the server via its Reality public key over uTLS-shaped
    TLS with a decoy certificate. A stock, unmodified Tailscale client cannot use
    the VLESS data plane.

## WebUI

The WebUI is **embedded in the same `stscale` binary** — there is no separate service or systemd unit to run. The systemd unit above already serves it on the `listen_addr` port. Visit `http://<server>:8080/web` or `/admin` — same for server and client. See `docs/usage/webui.md`.

## Upgrading from Headscale / old StealthScale

StealthScale's VLESS+Reality identity (`xray.secret`, `reality` keys, `NodeUUID`
/`NodePort`) is derived from the per-server secret. Changing `xray.secret` or
`xray.reality.dest` changes every node's `vless://` URI (`hscontrol/xray/vless.go:152`).

1. **Backup** `db.sqlite` and the sibling `.xray_secret` (next to the DB path,
   e.g. `/var/lib/stealthscale/.xray_secret`). For postgres, backup the
   `xray.secret` value from `config.yaml` — there is no local file.
2. **Keep `xray.secret` and `xray.reality.*` stable** across upgrades. If you
   must rotate the secret or dest, re-issue `vless://` URIs with
   `stscale nodes vless <id>` and redistribute to clients.
3. **Reality dest change** (`www.microsoft.com:443` → `www.cloudflare.com:443`)
   does not change `NodeUUID`/`NodePort` (only `pbk`/`sid`/`dest` in the URI),
   but clients must update their `--vless-uri` to the new `dest`/`pbk`/`sid` or
   the Reality handshake will fail (server presents new `PublicKey`/`ShortId`).
4. Do **not** rename columns that later migrations reference; add new migrations
   to the end of `hscontrol/db/db.go:962` (`202602201200-...` format) and never
   disable FKs (see `AGENTS.md`).

Test the path with an old `db.sqlite` volume before production:

```bash
cp /var/lib/stealthscale/db.sqlite /tmp/db.sqlite.old
# restore
cp /tmp/db.sqlite.old /var/lib/stealthscale/db.sqlite
cp /tmp/.xray_secret /var/lib/stealthscale/.xray_secret  # if sqlite
systemctl restart stealthscale
stscale nodes vless 1  # should be identical to before restart
```

`CHANGELOG.md` marks the Reality vendoring as breaking if `xray.secret` changes.

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
