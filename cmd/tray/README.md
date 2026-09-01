# Tray (Windows hide-in-tray — implemented alpha.3)

Windows system-tray is **implemented in alpha.3** (`hscontrol/tray/tray_windows.go`, called via `stscale serve --tray`). `cmd/tray/` remains as historical placeholder; the runtime tray lives in `hscontrol/tray` so `stscale serve --tray` is a single binary (no separate `tray.exe`).

- `hscontrol/tray/tray_windows.go` (`//go:build windows`) using `fyne.io/systray` (`golang.org/x/sys/windows` only, no CGO) + `github.com/toqueteos/webbrowser`
- Menu: Open WebUI (`http://127.0.0.1:8080/web` derived from `cfg.Addr`), Status poll (`/health` every 5s → Status: checking…/online/offline), Quit; close (X) quits via systray, `--tray` keeps service in tray
- Icon: `hscontrol/assets/tray.ico` (embedded ICO 256/48/32/16 via `hscontrol/assets/assets.go:TrayIcon`), tooltip `StealthScale <version>`
- Autostart via `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` (`packaging/windows/install.ps1 -LaunchAtStartup` writes `stscale serve --tray`)
- Packaging `zip` with `stscale.exe` via `.goreleaser.yml:39 windows_*` (MSI via WiX planned for beta)

WebUI remains primary control plane; tray is just launcher. See `packaging/windows/README.md` and `hscontrol/webui/webui.go:333`. `stscale serve --tray` is Windows-only; on Linux/macOS it warns and runs headless.
