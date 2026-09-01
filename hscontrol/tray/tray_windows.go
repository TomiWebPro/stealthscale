//go:build windows

package tray

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"fyne.io/systray"
	"github.com/rs/zerolog/log"
	"github.com/toqueteos/webbrowser"
	"github.com/tomiwebpro/stealthscale/hscontrol/assets"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
)

// Run starts the Windows system tray. It blocks until the user quits
// or ctx is cancelled. addr is the server listen address (e.g. 0.0.0.0:8080)
// used to build the WebUI URL. version is shown in the tooltip.
// onQuit is called when the user selects Quit.
func Run(ctx context.Context, cfg *types.Config, version string, onQuit func()) {
	webURL := buildWebURL(cfg)
	icon := assets.TrayIcon
	if len(icon) == 0 {
		icon = assets.Favicon
	}
	systray.Run(func() {
		if len(icon) > 0 {
			systray.SetIcon(icon)
		}
		tooltip := "StealthScale"
		if version != "" && version != "dev" {
			tooltip = fmt.Sprintf("StealthScale %s", version)
		}
		systray.SetTooltip(tooltip)

		mOpen := systray.AddMenuItem("Open WebUI", "Open StealthScale WebUI in browser")
		mStatus := systray.AddMenuItem("Status: checking…", "Stealth transport status")
		mStatus.Disable()
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("Quit", "Quit StealthScale")

		// Handle clicks
		go func() {
			for {
				select {
				case <-ctx.Done():
					systray.Quit()
					return
				case <-mOpen.ClickedCh:
					log.Info().Str("url", webURL).Msg("tray: open WebUI")
					if err := webbrowser.Open(webURL); err != nil {
						log.Error().Err(err).Str("url", webURL).Msg("tray: failed to open browser")
					}
				case <-mQuit.ClickedCh:
					log.Info().Msg("tray: quit requested")
					systray.Quit()
					if onQuit != nil {
						onQuit()
					}
					return
				}
			}
		}()

		// Poll health/status every 5s
		go func() {
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			updateStatus(mStatus, cfg)
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					updateStatus(mStatus, cfg)
				}
			}
		}()
	}, func() {
		// onExit - systray cleaned up
		log.Info().Msg("tray: exited")
		if onQuit != nil {
			// ensure server shutdown if tray exit was via window close
			// caller handles signal; we just log
		}
	})
}

func updateStatus(item *systray.MenuItem, cfg *types.Config) {
	addr := cfg.Addr
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		item.SetTitle("Status: unknown")
		return
	}
	// try health endpoint
	host := "127.0.0.1"
	url := fmt.Sprintf("http://%s:%s/health", host, port)
	if cfg.TLS.CertPath != "" || cfg.TLS.LetsEncrypt.Hostname != "" {
		url = fmt.Sprintf("https://%s:%s/health", host, port)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		item.SetTitle("Status: offline")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		// also try stealth check via /health? but just show online
		item.SetTitle("Status: online")
	} else {
		item.SetTitle(fmt.Sprintf("Status: %d", resp.StatusCode))
	}
}

// IsSupported reports whether tray is supported on this OS.
func IsSupported() bool { return true }

// NeedsTray reports whether cfg requests tray (always true for windows when called).
func NeedsTray(cfg *types.Config) bool { return true }
