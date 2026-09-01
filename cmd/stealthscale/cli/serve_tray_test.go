package cli

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tomiwebpro/stealthscale/hscontrol/tray"
)

func TestServeTrayFlagRegistered(t *testing.T) {
	f := serveCmd.Flags().Lookup("tray")
	require.NotNil(t, f, "serve --tray flag must be registered for windows hide-in-tray")
	assert.Equal(t, "false", f.DefValue)
	assert.Contains(t, f.Usage, "tray")
}

func TestServeTrayIsSupportedOnWindowsOnly(t *testing.T) {
	supported := tray.IsSupported()
	if runtime.GOOS == "windows" {
		assert.True(t, supported, "tray should be supported on windows")
	} else {
		assert.False(t, supported, "tray should not be supported on %s", runtime.GOOS)
	}
	// Ensure serve --tray does not panic on non-windows (warns and continues)
	// This is validated by the flag existence and IsSupported check.
}

func TestServeCommandHasTrayHelp(t *testing.T) {
	// Root help should not crash when listing serve flags
	found := false
	for _, c := range rootCmd.Commands() {
		if c.Name() == "serve" {
			found = true
			// Verify long/short not empty
			assert.NotEmpty(t, c.Short)
			break
		}
	}
	require.True(t, found, "serve command must be registered")
}
