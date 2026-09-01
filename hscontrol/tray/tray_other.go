//go:build !windows

package tray

import (
	"context"

	"github.com/rs/zerolog/log"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
)

// Run is a no-op on non-Windows. It logs and returns immediately.
func Run(ctx context.Context, cfg *types.Config, version string, onQuit func()) {
	log.Warn().Msg("tray: --tray is Windows only, ignoring")
	if onQuit != nil {
		<-ctx.Done()
	}
}

// IsSupported reports whether tray is supported on this OS.
func IsSupported() bool { return false }

// NeedsTray reports whether tray is needed; always false on non-Windows.
func NeedsTray(cfg *types.Config) bool { return false }
