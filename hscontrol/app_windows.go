//go:build windows

package hscontrol

import (
	"context"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/Microsoft/go-winio"
	"github.com/tomiwebpro/stealthscale/hscontrol/util"
)

// ensureUnixSocketIsAbsent is a no-op on Windows — named pipes do not leave
// filesystem entries.
func (h *StealthScale) ensureUnixSocketIsAbsent() error {
	return nil
}

// listenSocket creates the local control socket listener on Windows.
// It uses a Windows named pipe via go-winio. The socket path is expected to
// be a pipe path such as \\.\pipe\stealthscale or the URI form
// npipe:////./pipe/stealthscale.
func (h *StealthScale) listenSocket(_ context.Context) (net.Listener, error) {
	pipePath := h.cfg.UnixSocket
	if pipePath == "" {
		pipePath = `\\.\pipe\stealthscale`
	} else {
		pipePath = util.NormalizePipePath(pipePath)
		// If the config still contains a Unix-style path (e.g. from a Linux
		// config file copied to Windows), fall back to the default pipe.
		if !util.IsNamedPipe(pipePath) {
			pipePath = `\\.\pipe\stealthscale`
		}
	}
	// go-winio's ListenPipe takes the pipe path and optional security descriptor.
	// Passing nil uses the default descriptor (current user).
	return winio.ListenPipe(pipePath, nil)
}

// setupSignalNotify registers the signals relevant on Windows.
// SIGHUP does not exist on Windows; only SIGINT and SIGTERM are used for shutdown.
func setupSignalNotify(c chan os.Signal) {
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
}

// isSIGHUP always returns false on Windows — there is no SIGHUP policy reload.
func isSIGHUP(_ os.Signal) bool {
	return false
}
