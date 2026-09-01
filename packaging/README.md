# Packaging

Distribution packaging for StealthScale native binaries and Docker images (alpha.4 enables full Docker compatibility).

## Linux (systemd + deb)

- `packaging/systemd/stealthscale.service` — systemd unit with `StateDirectory`, `RuntimeDirectory`, `RestrictAddressFamilies=AF_UNIX`, etc.
- `packaging/deb/` — `postinst`/`prerm`/`postrm` scripts used by `goreleaser/nfpms` to build `deb`.
- The `goreleaser` `nfpms` stanza builds `deb` for `linux_amd64`, `linux_arm64`, `linux_arm` (32-bit, GOARM 6/7). `rpm`/`apk` can be added later.

## macOS (launchd)

- `packaging/launchd/com.stealthscale.plist` — `launchd` daemon plist: `Label=com.stealthscale`, `ProgramArguments=/usr/local/bin/stscale serve --config /usr/local/etc/stealthscale/config.yaml`, `RunAtLoad`, `KeepAlive`, `StandardOutPath`/`StandardErrorPath` under `/Library/Logs/stealthscale`, `EnvironmentVariables`.
- `packaging/launchd/install.sh` — `sudo ./install.sh [stscale-binary] [config.yaml]` copies the binary to `/usr/local/bin`, config to `/usr/local/etc/stealthscale`, state/logs to `/Library/Application Support/stealthscale` and `/Library/Logs/stealthscale`, and runs `launchctl load`.

```bash
GOOS=darwin GOARCH=arm64 go build -o stscale-darwin ./cmd/stealthscale
sudo ./packaging/launchd/install.sh ./stscale-darwin ./config-example.yaml
sudo launchctl load /Library/LaunchDaemons/com.stealthscale.plist
log stream --predicate 'process == "stscale"' --info
./stscale-darwin --address unix:///var/run/stealthscale/stealthscale.sock nodes list
```

`goreleaser` builds `darwin_amd64` and `darwin_arm64` `tar.gz` archives (no `dmg`/`pkg` in alpha).

## Windows (service + tray)

- `packaging/windows/install.ps1` — PowerShell installer for `%ProgramFiles%\stealthscale\stscale.exe` + `%ProgramData%\stealthscale\config.yaml` and a Windows service via `sc.exe`. The local API is exposed over named pipe `\\.\pipe\stealthscale` (`npipe:////./pipe/stealthscale` in CLI/config). `-LaunchAtStartup` adds `HKCU\...\Run` for `serve --tray`.
- `packaging/windows/uninstall.ps1` — clean uninstall (`-Purge` to delete `%ProgramData%\stealthscale`); also `stscale uninstall [--purge]`.
- `packaging/windows/README.md` — manual `sc.exe` fallback + tray (`serve --tray` via `fyne.io/systray`, Open WebUI, Status, Quit).

```powershell
GOOS=windows GOARCH=amd64 go build -o stscale.exe ./cmd/stealthscale
.\stscale.exe serve --config $env:ProgramData\stealthscale\config.yaml          # headless service
.\stscale.exe serve --tray --config $env:ProgramData\stealthscale\config.yaml   # hide-in-tray
.\stscale.exe --address npipe:////./pipe/stealthscale nodes list
.\stscale.exe uninstall --purge   # clean removal (also uninstall.ps1 -Purge)
```

`goreleaser` builds `windows_amd64` and `windows_arm64` `zip` archives.

## Docker (distroless via goreleaser kos + standalone Dockerfile)

- `Dockerfile` — production image (debian-trixie-slim, `stscale` + `stealthscale` symlink, volumes `/var/lib/stealthscale` + `/etc/stealthscale`, ports `8080`, `9090`, `3478/udp`, `10001-10100`, HEALTHCHECK `stscale health`).
- `Dockerfile.integration` / `Dockerfile.integration-ci` — integration test images (debian-slim with `delve`, same volumes/ports/healthcheck for CI).
- `goreleaser` `kos` builds `ghcr.io/tomiwebpro/stealthscale` for `linux/amd64` + `linux/arm64` (distroless `base-debian13` + `:debug` variant with busybox) with tags `latest`/`stable`/`unstable`/`v0.0.1-alpha.4`/`sha-*` (prerelease → `unstable`, stable → `latest+stable`).

```shell
# From GHCR (goreleaser kos)
docker run --name stscale --detach --read-only --tmpfs /var/run/stealthscale \
  --volume "$(pwd)/config:/etc/stealthscale:ro" --volume "$(pwd)/lib:/var/lib/stealthscale" \
  --publish 8080:8080 --publish 9090:9090 --publish 10001-10100:10001-10100 --publish 3478:3478/udp \
  ghcr.io/tomiwebpro/stealthscale:v0.0.1-alpha.4 serve
docker exec stscale stscale health
docker logs stscale

# Manual build
docker build -t stealthscale:local --build-arg VERSION=dev -f Dockerfile .
docker run --rm stealthscale:local version
```

Requirements for Docker: mount `/var/lib/stealthscale` as a volume so `db.sqlite`, `noise_private.key`, `derp_server_private.key` and `.xray_secret` (Reality identity) persist across restarts — otherwise VLESS UUID/ports will change. For `postgres`, set `STEALTHSCALE_XRAY_SECRET` explicitly (no local file).

## Uninstall (all distributions — clean)

- **Debian/Ubuntu deb**: `sudo apt remove stealthscale` (keep `/var/lib/stealthscale`) or `sudo apt purge stealthscale` (delete state, user). `packaging/deb/postrm:27 purge` removes data.
- **Linux manual/systemd**: `sudo ./packaging/systemd/uninstall.sh [--purge]` or `sudo stscale uninstall [--purge]` (stops/disables `stealthscale.service`, removes `/usr/bin/stscale`, `--purge` also deletes `/etc/stealthscale`, `/var/lib/stealthscale`).
- **macOS launchd**: `sudo ./packaging/launchd/uninstall.sh [--purge]` or `sudo stscale uninstall [--purge]` (unloads `com.stealthscale.plist`, removes `/usr/local/bin/stscale`, `/usr/local/etc/stealthscale`, `/Library/Application Support/stealthscale`; `--purge` also logs).
- **Windows**: `stscale uninstall [--purge]` or `powershell -ExecutionPolicy Bypass -File packaging/windows/uninstall.ps1 -Purge` (stops/deletes `StealthScale` service, removes `%ProgramFiles%\stealthscale`, `HKCU\...\Run`; `--purge` also `%ProgramData%\stealthscale`).
- **Generic**: `stscale uninstall --help` (prompts, use `--yes`/`--force` to skip).

## Platforms (alpha.4)

`goreleaser` builds all native OS/arch plus Docker (kos enabled):

```
linux_amd64, linux_arm64, linux_arm (6/7), darwin_amd64, darwin_arm64, windows_amd64, windows_arm64, freebsd_amd64, freebsd_arm64
+ docker: ghcr.io/tomiwebpro/stealthscale (linux/amd64, linux/arm64, distroless)
```

See `docs/setup/install/container.md` for container usage and `docs/about/versioning.md` for channel tags.
