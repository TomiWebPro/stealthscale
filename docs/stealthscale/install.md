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

### OS Paths (per `hscontrol/types/config.go:658-713`)

| OS | Config search | State / DB & `.xray_secret` | Socket | `tls_letsencrypt_cache_dir` default |
|---|---|---|---|---|
| Linux | `/etc/stealthscale` → `$HOME/.stealthscale` → `.` | `/var/lib/stealthscale/db.sqlite` + sibling `.xray_secret` | `/var/run/stealthscale/stealthscale.sock` | `/var/www/.cache` |
| macOS | `/usr/local/etc/stealthscale` → `/Library/Application Support/stealthscale` → `~/Library/Application Support/stealthscale` | `/var/lib/stealthscale` or `/Library/Application Support/stealthscale` | `/var/run/stealthscale/stealthscale.sock` | `~/Library/Caches/stealthscale` |
| Windows | `%ProgramData%\stealthscale` → `%APPDATA%\stealthscale` → `.` | `%ProgramData%\stealthscale\db.sqlite` | `\\.\pipe\stealthscale` (`npipe:////./pipe/stealthscale`) | `%ProgramData%\stealthscale\cache` |

Override with `STSCALE_CONFIG`/`-c` (`viper.SetConfigFile`). See `docs/ref/configuration.md`.

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
tls_letsencrypt_cache_dir: /var/www/.cache  # Linux default; macOS: ~/Library/Caches/stealthscale, Windows: %ProgramData%\stealthscale\cache (hscontrol/types/config.go:700-712)
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

=== "Linux (systemd)"

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

=== "macOS (launchd)"

    ```xml
    <!-- /Library/LaunchDaemons/com.stealthscale.plist (see packaging/launchd/com.stealthscale.plist) -->
    <key>Label</key><string>com.stealthscale</string>
    <key>ProgramArguments</key><array><string>/usr/local/bin/stscale</string><string>serve</string><string>--config</string><string>/usr/local/etc/stealthscale/config.yaml</string></array>
    <key>RunAtLoad</key><true/><key>KeepAlive</key><true/>
    ```
    ```bash
    sudo cp packaging/launchd/com.stealthscale.plist /Library/LaunchDaemons/
    sudo launchctl load /Library/LaunchDaemons/com.stealthscale.plist
    # brew (future): brew install stealthscale/tap/stscale
    ```

=== "Windows (Service + Named Pipe + Tray)"

    ```powershell
    # Native Windows — no Docker (see packaging/windows/README.md)
    powershell -ExecutionPolicy Bypass -File packaging/windows/install.ps1
    # with tray autostart:
    powershell -ExecutionPolicy Bypass -File packaging/windows/install.ps1 -LaunchAtStartup
    # tray mode (hide-in-tray, alpha.3): systray icon with Open WebUI / Status / Quit
    & "C:\Program Files\stealthscale\stscale.exe" serve --tray --config "%ProgramData%\stealthscale\config.yaml"
    # or: sc.exe create stealthscale binPath= ""C:\Program Files\stealthscale\stscale.exe" serve --config "%ProgramData%\stealthscale\config.yaml""
    sc.exe start stealthscale
    # CLI via named pipe:
    .\stscale.exe --address npipe:////./pipe/stealthscale nodes list
    # uninstall (clean):
    .\stscale.exe uninstall --purge
    powershell -ExecutionPolicy Bypass -File packaging/windows/uninstall.ps1 -Purge
    ```

## Create User & PreAuth Key

```bash
stscale users create alice
stscale preauthkeys create --user <id> --reusable --expiration 24h
# or: stscale apikeys create --expiration 90d   # for WebUI/API
```

## Connect Client (Unified `stscale up`)

Use the unified `stscale up` (recommended). Get the VLESS URI on the coordinator via `stscale nodes vless <id>` and paste to the client:

```bash
# On coordinator: get VLESS URI for node 1
stscale nodes vless 1
# => vless://<uuid>@<addr>:<port>?security=reality_xtls&pbk=...&sid=...&dest=...&fp=chrome&type=tcp&flow=xtls-rprx-vision

# On client: connect via stealth transport (see `stscale up --help` flags --coordinator/--authkey/--vless-uri/--endpoint)
stscale up --coordinator https://ctl.example.com --authkey <preauthkey> --vless-uri 'vless://<uuid>@<addr>:<port>?security=reality_xtls&pbk=...&sid=...&dest=...&fp=chrome'
```

Legacy: patched `tailscale up` (reference only) — requires `xray.stealth.enforce_control:false` for stock client compat and `hscontrol/xray.DialVLESS` patch. See `docs/client-modification.md` and `docs/stealthscale/clients.md` for `client/patch/direct.go.diff` reference.

Discovery vs stealth: plain `GET /key` is non-stealth discovery (allowed); registration (`VLESS+Noise`) must be stealth. DERP fail-closed gated on `stealth_satisfied` — check `curl /web/api/derp`.

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

## Uninstall (clean, all distributions)

StealthScale ships `stscale uninstall` (cross-platform) plus native scripts — all require elevation for service removal (`sudo` / Admin PowerShell).

```bash
# Linux (systemd/deb): keeps data by default
sudo stscale uninstall              # stop service, remove binary, keep /etc/stealthscale & /var/lib/stealthscale
sudo stscale uninstall --purge      # also delete config, db, user; for deb also: sudo apt purge stealthscale
sudo ./packaging/systemd/uninstall.sh --purge   # manual alternative

# macOS (launchd): keeps config/state
sudo stscale uninstall              # unload plist, remove binary, keep config/state
sudo stscale uninstall --purge      # also delete /usr/local/etc/stealthscale, /Library/Application Support/stealthscale, logs
sudo ./packaging/launchd/uninstall.sh --purge

# Windows (PowerShell Admin):
.\stscale.exe uninstall             # stop/delete service, remove %ProgramFiles%\stealthscale, keep %ProgramData%\stealthscale
.\stscale.exe uninstall --purge     # also delete config, db.sqlite, .xray_secret
powershell -ExecutionPolicy Bypass -File packaging/windows/uninstall.ps1 -Purge
```

Use `--yes` / `-f` to skip the confirmation prompt. The named pipe `\\.\pipe\stealthscale` and `/var/run/stealthscale.sock` are ephemeral (no file to delete). See `packaging/README.md`.

## Update Checks

Scheduler daily pushes code once per day; to update server:

```bash
git pull # daily-push already did at 02:00 UTC
make build && sudo systemctl restart stealthscale
```
