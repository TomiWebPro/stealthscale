# Packaging

Distribution packaging for StealthScale native binaries (no Docker until Windows+macOS layers are ready).

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

## Windows (service, no Docker)

- `packaging/windows/install.ps1` — PowerShell installer for `%ProgramFiles%\stealthscale\stscale.exe` + `%ProgramData%\stealthscale\config.yaml` and a Windows service via `sc.exe`. The local API is exposed over named pipe `\\.\pipe\stealthscale` (`npipe:////./pipe/stealthscale` in CLI/config).
- `packaging/windows/README.md` — manual `sc.exe` fallback docs.

```powershell
GOOS=windows GOARCH=amd64 go build -o stscale.exe ./cmd/stealthscale
.\stscale.exe serve --config $env:ProgramData\stealthscale\config.yaml
.\stscale.exe --address npipe:////./pipe/stealthscale nodes list
```

`goreleaser` builds `windows_amd64` and `windows_arm64` `zip` archives.

## Next alpha

`goreleaser` targets all OS/arch except Docker (`kos` stays disabled until native Windows+macOS are proven):

```
linux_amd64, linux_arm64, linux_arm (6/7), darwin_amd64, darwin_arm64, windows_amd64, windows_arm64, freebsd_amd64, freebsd_arm64
```

`kos` will be re-enabled after native ports are stable.
