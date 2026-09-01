package cli

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUninstallCommandRegistered(t *testing.T) {
	// uninstall is registered on rootCmd for all GOOS
	found := false
	for _, c := range rootCmd.Commands() {
		if c.Name() == "uninstall" {
			found = true
			// Flags
			assert.NotNil(t, c.Flags().Lookup("purge"), "uninstall should have --purge")
			assert.NotNil(t, c.Flags().Lookup("yes"), "uninstall should have --yes")
			break
		}
	}
	require.True(t, found, "uninstall command must be registered")
}

func TestUninstallHelpPerOS(t *testing.T) {
	// Ensure help text mentions OS-specific paths and does not panic
	for _, c := range rootCmd.Commands() {
		if c.Name() == "uninstall" {
			_ = c
			break
		}
	}
	// Just validate runtime branching does not panic
	switch runtime.GOOS {
	case "linux":
		assert.Contains(t, uninstallCmd.Long, "Linux", "help should mention Linux")
	case "windows":
		assert.Contains(t, uninstallCmd.Long, "Windows", "help should mention Windows")
	case "darwin":
		assert.Contains(t, uninstallCmd.Long, "macOS", "help should mention macOS")
	default:
		assert.Contains(t, uninstallCmd.Long, "uninstall")
	}
}


