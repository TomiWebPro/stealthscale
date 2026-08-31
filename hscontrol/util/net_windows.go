//go:build windows

package util

import (
	"context"
	"net"
	"strings"

	"github.com/Microsoft/go-winio"
)

// SocketDialer dials the stealthscale control socket.
// On Windows it uses a named pipe (//./pipe/stealthscale) via go-winio.
// It accepts both the raw pipe path (\\.\pipe\stealthscale) and the
// URI form (npipe:////./pipe/stealthscale) used in config/CLI.
func SocketDialer(ctx context.Context, addr string) (net.Conn, error) {
	pipe := normalizePipePath(addr)
	return winio.DialPipeContext(ctx, pipe)
}

// IsNamedPipe reports whether addr is a Windows named-pipe address.
func IsNamedPipe(addr string) bool {
	return strings.HasPrefix(addr, `\\.\pipe\`) ||
		strings.HasPrefix(addr, `\\`) ||
		strings.HasPrefix(addr, "npipe://")
}

// SocketNetwork returns the network name for the given socket address.
func SocketNetwork(_ string) string {
	return "pipe"
}

// NormalizePipePath converts npipe:// URIs and forward-slash forms to
// the canonical \\.\pipe\... form required by go-winio. Exported for use
// by the server listener.
func NormalizePipePath(addr string) string {
	return normalizePipePath(addr)
}

// normalizePipePath converts npipe:// URIs and forward-slash forms to
// the canonical \\.\pipe\... form required by go-winio.
func normalizePipePath(addr string) string {
	if strings.HasPrefix(addr, "npipe://") {
		trimmed := strings.TrimPrefix(addr, "npipe://")
		// npipe:////./pipe/stealthscale -> //./pipe/stealthscale
		// Convert forward slashes to backslashes.
		trimmed = strings.ReplaceAll(trimmed, "/", `\`)
		// Ensure it starts with \\.\
		if strings.HasPrefix(trimmed, `\\.\`) {
			return trimmed
		}
		if strings.HasPrefix(trimmed, `\\.`) {
			return trimmed
		}
		// //./pipe/... -> \\.\pipe\...
		if strings.HasPrefix(trimmed, `\\`) {
			return trimmed
		}
		// Handle case where trimmed is \\.\pipe\... already after replace.
		return trimmed
	}
	// Already a pipe path; normalise forward slashes.
	addr = strings.ReplaceAll(addr, "/", `\`)
	return addr
}
