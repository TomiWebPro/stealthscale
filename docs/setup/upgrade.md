# Upgrade an existing StealthScale installation

!!! tip "Required update path"

    It's required to update from one stable version to the next (e.g. 0.26.0 → 0.27.1 → 0.28.0) without skipping minor
    versions in between. You should always pick the latest available patch release.

Update an existing StealthScale installation:

- Read the [GitHub releases](https://github.com/tomiwebpro/stealthscale/releases) announcement for the new version (breaking changes, especially any `xray.secret` / `xray.reality.dest` handling).
- Stop StealthScale
- **[Create a backup of your installation](#backup)** — including `xray.secret` / `.xray_secret` so VLESS URIs stay stable
- Update StealthScale to the new version (same install method)
- Compare and update the [configuration](../ref/configuration.md) and [XRay/VLESS reference](../ref/xray-vless.md) (`xray.listen_port` … `max_listen_port`, `xray.reality.dest`/`server_names`/`short_ids`/`spider_x`, `xray.stealth.enforce`/`enforce_control`)
- Start StealthScale and verify `stscale nodes vless <id>` is identical to before (`vless://` `pbk`/`sid`/`dest` must not change if `xray.secret` is stable) and `curl http://127.0.0.1:9090/metrics | grep stealth_ready`

See also [StealthScale Install & deploy](../stealthscale/install.md#upgrading-from-headscale--old-stealthscale) for the VLESS identity stability rules.

## Backup

StealthScale applies database migrations during upgrades (`hscontrol/db/db.go:962` — never reorder, only append `YYYYMMDDHHMM-short-desc`, never disable FKs). We highly recommend a backup before upgrading. A full backup depends on your setup:

=== "Standard installation (sqlite)"

    Standard install (from [official releases](install/official.md) or [StealthScale install](../stealthscale/install.md)) uses:

    - Config: `/etc/stealthscale/config.yaml`
    - Data dir: `/var/lib/stealthscale` (or `/var/lib/coordination` for current deb, per `packaging/systemd/stealthscale.service`)
    - SQLite: `/var/lib/stealthscale/db.sqlite`
    - VLESS identity: `/var/lib/stealthscale/.xray_secret` — **must be backed up** (next to `db.sqlite`), otherwise `NodeUUID`/`NodePort` and `public_key`/`shortId` change

    ```console
    TIMESTAMP=$(date +%Y%m%d%H%M%S)
    cp -aR /etc/stealthscale /etc/stealthscale.backup-$TIMESTAMP
    cp -aR /var/lib/stealthscale /var/lib/stealthscale.backup-$TIMESTAMP
    # verify .xray_secret is in the backup:
    ls -la /var/lib/stealthscale.backup-$TIMESTAMP/.xray_secret
    ```

    Restore and verify:

    ```console
    cp /tmp/db.sqlite.old /var/lib/stealthscale/db.sqlite
    cp /tmp/.xray_secret /var/lib/stealthscale/.xray_secret
    systemctl restart stealthscale
    stscale nodes vless 1  # should be identical to before
    ```

=== "Container"

    ```console
    cp -aR /path/to/stscale /path/to/stscale.backup-$(date +%Y%m%d%H%M%S)
    # includes .xray_secret if volume is /var/lib/stealthscale
    ```

=== "PostgreSQL"

    Follow PostgreSQL [Backup and Restore](https://www.postgresql.org/docs/current/backup.html). **Also backup `xray.secret` from `config.yaml`** — there is no local `.xray_secret` file, and `stealthscale` will refuse to start without it (`xray.secret is required when database.type is postgres`). Keep `xray.reality.dest`/`server_names`/`short_ids` stable too or clients must update `--vless-uri`.

Changing `xray.reality.dest` (e.g. `www.microsoft.com:443` → `www.cloudflare.com:443`) does **not** change `NodeUUID`/`NodePort` but does change `pbk`/`sid`/`dest` in the `vless://` URI — clients must update their `--vless-uri`.
