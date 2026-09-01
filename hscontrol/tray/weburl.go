package tray

import (
	"fmt"
	"net"

	"github.com/tomiwebpro/stealthscale/hscontrol/types"
)

// buildWebURL returns the WebUI URL derived from cfg.Addr.
// It normalizes 0.0.0.0/:: to 127.0.0.1 and picks http vs https based on TLS.
func buildWebURL(cfg *types.Config) string {
	addr := cfg.Addr
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://127.0.0.1:8080/web"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	scheme := "http"
	if cfg.TLS.CertPath != "" || cfg.TLS.LetsEncrypt.Hostname != "" {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s:%s/web", scheme, host, port)
}
