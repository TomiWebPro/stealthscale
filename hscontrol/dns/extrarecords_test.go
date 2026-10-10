// Copyright (c) 2026 TomiWebPro <TomiWebPro@gmail.com>
// SPDX-License-Identifier: BSD-3-Clause

package dns

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"tailscale.com/tailcfg"
)

func TestReadExtraRecordsFromPath(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()

	validContent := `[{"name":"ns1.example.com","type":"A","value":"100.64.0.1"},{"name":"ns2.example.com","type":"AAAA","value":"::1"}]`

	t.Run("valid JSON array of records", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(tmp, "valid.json")
		require.NoError(t, os.WriteFile(path, []byte(validContent), 0o644))

		records, hash, err := readExtraRecordsFromPath(path)
		require.NoError(t, err)
		assert.NotEmpty(t, hash, "hash must be non-zero for valid input")
		assert.Len(t, records, 2, "must parse two records")
		assert.Equal(t, tailcfg.DNSRecord{
			Name:  "ns1.example.com",
			Type:  "A",
			Value: "100.64.0.1",
		}, records[0])
	})

	t.Run("empty file returns nil without error", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(tmp, "empty.json")
		require.NoError(t, os.WriteFile(path, []byte{}, 0o644))

		records, hash, err := readExtraRecordsFromPath(path)
		require.NoError(t, err, "empty file must not error")
		assert.Nil(t, records, "empty file must return nil records")
		assert.Empty(t, hash, "empty file must return zero hash")
	})

	t.Run("invalid JSON returns error with content", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(tmp, "invalid.json")
		require.NoError(t, os.WriteFile(path, []byte(`{"not":"an object"}`), 0o644))

		_, _, err := readExtraRecordsFromPath(path)
		assert.Error(t, err, "invalid JSON structure must error")
		assert.Contains(t, err.Error(), "unmarshalling records", "error must mention unmarshalling")
	})

	t.Run("missing file returns error", func(t *testing.T) {
		t.Parallel()

		_, _, err := readExtraRecordsFromPath(filepath.Join(tmp, "nonexistent.json"))
		assert.Error(t, err, "missing file must return error")
		assert.Contains(t, err.Error(), "reading path", "error must mention reading path")
	})

	t.Run("JSON null yields empty records", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(tmp, "null.json")
		require.NoError(t, os.WriteFile(path, []byte(`null`), 0o644))

		records, _, err := readExtraRecordsFromPath(path)
		require.NoError(t, err, "JSON null should produce nil records without error")
		assert.Nil(t, records, "null JSON must yield nil records slice")
	})
}

func TestExtraRecordsManager_HashDeduplication(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()

	path := filepath.Join(tmp, "extra_records.json")

	require.NoError(t, os.WriteFile(path,
		[]byte(`[{"name":"ns1.example.com","type":"A","value":"100.64.0.1"}]`), 0o644))

	manager, err := NewExtraRecordsManager(path)
	require.NoError(t, err)
	defer manager.Close()

	// Initial load
	records := manager.Records()
	assert.Len(t, records, 1)

	// Write identical content (same hash) — updateRecords should skip silently
	require.NoError(t, os.WriteFile(path,
		[]byte(`[{"name":"ns1.example.com","type":"A","value":"100.64.0.1"}]`), 0o644))

	done := make(chan struct{})
	go func() {
		manager.updateRecords() // direct call, bypasses watcher delay
		close(done)
	}()

	select {
	case <-done:
		// updateRecords should return quickly even though content didn't change
	case <-time.After(500 * time.Millisecond):
		t.Fatal("updateRecords hung on unchanged content")
	}

	// Records should remain unchanged (hash dedup works)
	assert.Equal(t, records, manager.Records())
}

// TestNewExtraRecordsManager_ErrorPaths pins the constructor's
// failure modes (missing path, directory, invalid JSON) and, on
// Linux, asserts they do not leak a file descriptor: the fsnotify
// watcher owns an inotify fd that must be closed on every error
// path, otherwise a bad ExtraRecordsPath leaks one fd per startup
// attempt (NewExtraRecordsManager is called from app.go on every
// start).
func TestNewExtraRecordsManager_ErrorPaths(t *testing.T) {
	tmp := t.TempDir()

	invalidPath := filepath.Join(tmp, "invalid.json")
	require.NoError(t, os.WriteFile(invalidPath, []byte(`{bad json`), 0o600))

	openFDs := func() int {
		entries, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			return -1
		}

		return len(entries)
	}

	before := openFDs()

	for _, path := range []string{
		filepath.Join(tmp, "nonexistent.json"),
		tmp, // directory — ErrPathIsDirectory
		invalidPath,
	} {
		_, err := NewExtraRecordsManager(path)
		require.Error(t, err, "path %q must fail construction", path)
	}

	if runtime.GOOS == "linux" && before >= 0 {
		// Each leaked watcher costs one inotify fd; three failed
		// constructors must not grow the fd table.
		assert.LessOrEqual(t, openFDs(), before+1,
			"failed constructors must not leak the fsnotify watcher fd")
	}
}
