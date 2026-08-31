# Community packages (StealthScale)

Community packages may be used instead of [official releases](official.md) but keep StealthScale's stealth defaults: `xray.enabled:true`, `xray.security:reality_xtls` (via `github.com/xtls/reality` + `utls`), per-node VLESS listeners on `xray.listen_port` … `max_listen_port` (default `10001`–`10100`), and `xray.secret` / `.xray_secret` stability. The authoritative guide is [StealthScale Install & deploy](../../stealthscale/install.md). Community builds are not verified by the StealthScale authors and may be outdated — check `xray.*` and `stealth.*` in `/etc/stealthscale/config.yaml` after install.

!!! warning "May be outdated — verify VLESS+Reality"

    Run `stscale nodes vless 1` after install and confirm `security=reality_xtls` and that the VLESS port range is exposed. If `stscale` refuses to start on `postgres` with `xray.secret is required`, set `xray.secret` (`openssl rand -hex 32`). For WebUI, visit `/web` or `/admin` — it is embedded (not a separate package), see [Web UI](../../usage/webui.md) and [Integration Web UI](../../ref/integration/web-ui.md).

[![Packaging status](https://repology.org/badge/vertical-allrepos/stscale.svg)](https://repology.org/project/stscale/versions)

## Arch Linux

```shell
pacman -S stscale  # or stealthscale if renamed by AUR — verify binary is `stscale`
stscale --help
# check VLESS
stscale configtest && stscale nodes vless 1
```

## Fedora / RHEL / CentOS

COPR `jonathanspw/stscale`: <https://copr.fedorainfracloud.org/coprs/jonathanspw/stscale/> — follow its setup, then verify `xray.*` as above.

## Nix / NixOS

`stscale` (check `nixpkgs` for `stealthscale` alias):

```shell
nix-shell -p stscale
# or NixOS module: services.stealthscale.enable (adjust per actual nix expression)
```

`nix develop` in the StealthScale repo pins Go `1.26.1` via `flake.nix`.

## Gentoo

```shell
emerge --ask net-vpn/stscale
```

Per-port docs: <https://wiki.gentoo.org/wiki/User:Maffblaster/Drafts/StealthScale> — adapt for VLESS ports.

## OpenBSD

Ports: `pkg_add stscale` (service via `rc.d`). Follow [Build from source](source.md) if the port lags.

After any community install, continue at [StealthScale clients](../../stealthscale/clients.md) (`stscale up --coordinator --vless-uri`) and backup `.xray_secret` per [Upgrade](../upgrade.md).
