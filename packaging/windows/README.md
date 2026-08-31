# Windows Packaging (native, no Docker)

This directory contains native Windows installation assets for `stscale.exe`.
`stscale serve` runs as a Windows service via named pipe `\\.\pipe\stealthscale`
(no `AF_UNIX`, no `C:\var\run`). The WebUI remains at `http://127.0.0.1:8080`.

## Files

- `install.ps1` — PowerShell installer: copies `stscale.exe` to
  `%ProgramFiles%\stealthscale\`, config to `%ProgramData%\stealthscale\config.yaml`,
  creates/updates a Windows service (`sc.exe create StealthScale`), and
  optionally sets `HKCU\...\Run` for `--tray` autostart.
- The service runs `stscale serve --config %ProgramData%\stealthscale\config.yaml`
  and exposes the local API over `\\.\pipe\stealthscale` for `stscale --address npipe:////./pipe/stealthscale ...`.

## Manual `sc.exe` fallback (no installer)

```powershell
# Build
GOOS=windows GOARCH=amd64 go build -o stscale.exe ./cmd/stealthscale

# Install service (admin PowerShell)
sc.exe create StealthScale binPath= "\"C:\Program Files\stealthscale\stscale.exe\" serve --config \"C:\ProgramData\stealthscale\config.yaml\"" start= auto DisplayName= "StealthScale"
sc.exe description StealthScale "StealthScale — VLESS+Reality control plane"
sc.exe failure StealthScale reset= 86400 actions= restart/5000
sc.exe start StealthScale

# CLI via named pipe
.\stscale.exe --address npipe:////./pipe/stealthscale nodes list
```

## Goreleaser

`goreleaser` builds `windows_amd64` and `windows_arm64` `stscale.exe`
archives as `zip` (no MSI in alpha). Future MSI via `WiX` can be added
under `nfpms` when needed.

## Tray (future)

`windows-hide-in-tray` (post-alpha) will add `stscale serve --tray`
via `systray`. The `install.ps1 -LaunchAtStartup` flag already writes the
`HKCU\...\Run` key for it so the service + tray share the same pipe.
