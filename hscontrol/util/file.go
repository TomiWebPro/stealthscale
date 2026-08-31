package util

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/spf13/viper"
)

const (
	Base8              = 8
	Base10             = 10
	BitSize16          = 16
	BitSize32          = 32
	BitSize64          = 64
	PermissionFallback = 0o700
)

// ErrDirectoryPermission is returned when creating a directory fails due to permission issues.
var ErrDirectoryPermission = errors.New("creating directory failed with permission error")

func AbsolutePathFromConfigPath(path string) string {
	if path == "" {
		return path
	}
	// filepath.IsAbs handles all OS-specific absolute forms:
	// - Unix: /var/lib/...
	// - Windows: C:\..., C:/..., \\server\share, \\?\C:\...
	// - Clean normalises separators and dot segments.
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	// Relative path: resolve against the config file directory.
	dir, _ := filepath.Split(viper.ConfigFileUsed())
	if dir != "" {
		path = filepath.Join(dir, path)
	}
	return filepath.Clean(path)
}

func GetFileMode(key string) fs.FileMode {
	modeStr := viper.GetString(key)

	mode, err := strconv.ParseUint(modeStr, Base8, BitSize64)
	if err != nil {
		return PermissionFallback
	}

	return fs.FileMode(mode) //nolint:gosec // file mode is bounded by ParseUint
}

func EnsureDir(dir string) error {
	if _, err := os.Stat(dir); os.IsNotExist(err) { //nolint:noinlineerr
		err := os.MkdirAll(dir, PermissionFallback)
		if err != nil {
			if errors.Is(err, os.ErrPermission) {
				return fmt.Errorf("%w: %s", ErrDirectoryPermission, dir)
			}

			return fmt.Errorf("creating directory %s: %w", dir, err)
		}
	}

	return nil
}
