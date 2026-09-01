//go:build !windows

package hscontrol

import (
	"context"
	"errors"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/tomiwebpro/stealthscale/hscontrol/util"
)

// ensureUnixSocketIsAbsent will check if the given path for stealthscales unix socket is clear
// and will remove it if it is not.
func (h *StealthScale) ensureUnixSocketIsAbsent() error {
	if _, err := os.Stat(h.cfg.UnixSocket); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return os.Remove(h.cfg.UnixSocket)
}

// listenSocket creates the local control socket listener.
// On Unix it uses AF_UNIX with directory creation and chmod.
func (h *StealthScale) listenSocket(ctx context.Context) (net.Listener, error) {
	if err := h.ensureUnixSocketIsAbsent(); err != nil {
		return nil, err
	}
	socketDir := filepath.Dir(h.cfg.UnixSocket)
	if err := util.EnsureDir(socketDir); err != nil {
		return nil, err
	}
	lis, err := new(net.ListenConfig).Listen(ctx, "unix", h.cfg.UnixSocket)
	if err != nil {
		return nil, err
	}
	// Enforce owner-only permissions even if historic config still has 0770.
	// Group/other bits are masked to prevent container-breakout privesc via
	// membership in stealthscale/docker/www-data groups. Directory is 0700
	// via EnsureDir; socket file itself is 0600 (or 0700 masked).
	perm := h.cfg.UnixSocketPermission.Perm() & 0o777
	// Strip group/other entirely
	perm &^= 0o077
	if perm == 0 {
		perm = 0o600
	} else if perm&0o600 != 0o600 {
		perm |= 0o600
		perm &^= 0o077
	}
	if err := os.Chmod(h.cfg.UnixSocket, perm); err != nil {
		lis.Close()
		return nil, err
	}
	return lis, nil
}

// setupSignalNotify registers the signals relevant on Unix.
// SIGHUP triggers policy reload; SIGINT/SIGTERM/SIGQUIT trigger shutdown.
func setupSignalNotify(c chan os.Signal) {
	signal.Notify(c, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
}

// isSIGHUP reports whether sig is SIGHUP (policy-reload signal on Unix).
func isSIGHUP(sig os.Signal) bool {
	return sig == syscall.SIGHUP
}
