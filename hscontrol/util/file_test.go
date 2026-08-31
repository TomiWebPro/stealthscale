package util

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAbsolutePathFromConfigPath(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		configFile string
		want       string
		skipOS     string // skip on this GOOS
		onlyOS     string // only run on this GOOS
	}{
		{
			name:       "empty path returns empty",
			path:       "",
			configFile: "/etc/stealthscale/config.yaml",
			want:       "",
		},
		{
			name:       "unix absolute path is cleaned",
			path:       "/var/lib/stealthscale/db.sqlite",
			configFile: "/etc/stealthscale/config.yaml",
			want:       "/var/lib/stealthscale/db.sqlite",
		},
		{
			name:       "unix absolute with dots cleaned",
			path:       "/var/lib/../lib/stealthscale/./db.sqlite",
			configFile: "/etc/stealthscale/config.yaml",
			want:       "/var/lib/stealthscale/db.sqlite",
			onlyOS:     "linux",
		},
		{
			name:       "relative path joined with config dir",
			path:       "relative/path.yaml",
			configFile: "/etc/stealthscale/config.yaml",
			want:       filepath.Join("/etc/stealthscale", "relative/path.yaml"),
		},
		{
			name:       "relative without config dir returns cleaned",
			path:       "relative.yaml",
			configFile: "",
			want:       "relative.yaml",
		},
		{
			name:       "forward slash abs on windows is still abs",
			path:       "/var/lib/stealthscale/db.sqlite",
			configFile: "/etc/stealthscale/config.yaml",
			want:       filepath.Clean("/var/lib/stealthscale/db.sqlite"),
		},
	}

	// Windows-specific cases: only run when GOOS=windows so filepath.IsAbs handles drive letters.
	windowsTests := []struct {
		name       string
		path       string
		configFile string
		want       string
	}{
		{
			name:       "windows drive letter forward slash",
			path:       "C:/a.yaml",
			configFile: "C:/etc/stealthscale/config.yaml",
			want:       filepath.Clean("C:/a.yaml"),
		},
		{
			name:       "windows drive letter backslash",
			path:       `C:\a\b.yaml`,
			configFile: `C:\etc\stealthscale\config.yaml`,
			want:       filepath.Clean(`C:\a\b.yaml`),
		},
		{
			name:       "windows extended path",
			path:       `\\?\C:\a\b.yaml`,
			configFile: `C:\etc\config.yaml`,
			want:       filepath.Clean(`\\?\C:\a\b.yaml`),
		},
		{
			name:       "windows unc share",
			path:       `\\server\share\a.yaml`,
			configFile: `C:\etc\config.yaml`,
			want:       filepath.Clean(`\\server\share\a.yaml`),
		},
		{
			name:       "windows relative with drive config",
			path:       `relative.yaml`,
			configFile: `C:\ProgramData\stealthscale\config.yaml`,
			want:       filepath.Join(`C:\ProgramData\stealthscale`, `relative.yaml`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.skipOS != "" && runtime.GOOS == tt.skipOS {
				t.Skipf("skipped on %s", tt.skipOS)
			}
			if tt.onlyOS != "" && runtime.GOOS != tt.onlyOS {
				t.Skipf("only on %s", tt.onlyOS)
			}
			viper.Reset()
			if tt.configFile != "" {
				viper.SetConfigFile(tt.configFile)
			}
			got := AbsolutePathFromConfigPath(tt.path)
			assert.Equal(t, tt.want, got)
		})
	}

	if runtime.GOOS == "windows" {
		for _, tt := range windowsTests {
			t.Run("windows/"+tt.name, func(t *testing.T) {
				viper.Reset()
				if tt.configFile != "" {
					viper.SetConfigFile(tt.configFile)
				}
				got := AbsolutePathFromConfigPath(tt.path)
				// On Windows, Clean normalises separators.
				require.Equal(t, tt.want, got)
				assert.True(t, filepath.IsAbs(tt.path), "case path should be absolute on windows: %q", tt.path)
			})
		}
	} else {
		// Cross-check: on non-windows, verify IsAbs detection for windows paths is as expected.
		// This documents the bug that was fixed: previously strings.HasPrefix with '/' missed C:/.
		t.Run("cross-check windows drive not abs on linux (expected)", func(t *testing.T) {
			// On Linux, filepath.IsAbs does NOT consider C:/ as absolute — correct per GOOS.
			// The fix relies on GOOS=windows compilation to get correct semantics.
			assert.False(t, filepath.IsAbs("C:/a.yaml"))
			assert.True(t, filepath.IsAbs("/var/lib/stealthscale/db.sqlite"))
		})
	}
}

func TestAbsolutePathFromConfigPath_TableDriven(t *testing.T) {
	// Additional table covering the issue's required cases generically (GOOS-independent where possible).
	cases := []struct {
		input string
		abs   bool // whether input is absolute on current GOOS
	}{
		{input: "/var/lib/stealthscale/db.sqlite", abs: true},
		{input: "a/b", abs: false},
		{input: "", abs: false},
	}
	for _, c := range cases {
		isAbs := filepath.IsAbs(c.input)
		assert.Equal(t, c.abs, isAbs, "IsAbs mismatch for %q", c.input)
		viper.Reset()
		viper.SetConfigFile("/etc/stealthscale/config.yaml")
		got := AbsolutePathFromConfigPath(c.input)
		if c.input == "" {
			assert.Equal(t, "", got)
		} else if isAbs {
			assert.Equal(t, filepath.Clean(c.input), got)
		} else {
			assert.Equal(t, filepath.Join("/etc/stealthscale", c.input), got)
		}
	}
}
