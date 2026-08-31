//go:build !windows

package util

import (
	"context"
	"net"
	"strings"
)

// SocketDialer dials a local unix-domain socket, letting the HTTP CLI client
// reach the stealthscale API over its unix socket.
func SocketDialer(ctx context.Context, addr string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", addr)
}

// IsNamedPipe reports whether addr is a Windows named-pipe address.
// Always false on Unix.
func IsNamedPipe(addr string) bool {
	return strings.HasPrefix(addr, `\\.\pipe\`) || strings.HasPrefix(addr, "npipe://")
}

// SocketNetwork returns the network name for the given socket address.
func SocketNetwork(addr string) string {
	if IsNamedPipe(addr) {
		return "pipe"
	}
	return "unix"
}

// NormalizePipePath is a no-op on Unix; it returns addr unchanged.
func NormalizePipePath(addr string) string {
	return addr
}
