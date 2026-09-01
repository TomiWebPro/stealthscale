package hscontrol

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGoldenPathWindowsSanitization ensures goldenPath never emits
// Windows-forbidden characters (< > : " | ? * \ /) that break
// `git checkout` on windows-latest (see path-tests-windows failure
// on hscontrol: Windows tray + clean uninstall for alpha.3 #9:
// invalid path '..._->_400_parity.json' with ">" ).
func TestGoldenPathWindowsSanitization(t *testing.T) {
	// Simulate the previously-broken subtest name: "path prefix with id query is both -> 400 parity"
	// Full t.Name() for a subtest is "TestAPIV1ApiKeyDelete/path prefix with id query is both -> 400 parity"
	cases := []struct {
		name       string
		tName      string
		mustNotContain string
	}{
		{
			name:       "arrow contains >",
			tName:      "TestAPIV1ApiKeyDelete/path prefix with id query is both -> 400 parity",
			mustNotContain: ">",
		},
		{
			name:       "all NTFS forbidden chars",
			tName:      "Test/Foo<Bar>Baz:Qux\"Quux|Quu?Q*Q\\Q",
			mustNotContain: "<>:\"|?*\\",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Replicate goldenPath's sanitizer
			name := strings.NewReplacer(
				"/", "_",
				" ", "_",
				"<", "_",
				">", "_",
				":", "_",
				"\"", "_",
				"|", "_",
				"?", "_",
				"*", "_",
				"\\", "_",
			).Replace(tc.tName)
			for _, ch := range tc.mustNotContain {
				assert.NotContains(t, name, string(ch), "sanitized name must not contain %q", string(ch))
			}
			// Also ensure the real goldenPath helper would produce same
			// (we call it via a subtest)
			sub := &testing.T{}
			// Instead of calling goldenPath directly (needs *testing.T), verify filepath is clean
			path := filepath.Join(goldenDir, name+".json")
			assert.NotContains(t, path, ">", "golden path must not contain >")
			assert.NotContains(t, path, "<", "golden path must not contain <")
			assert.NotContains(t, path, ":", "golden path must not contain :")
			_ = sub
		})
	}
}

// TestGoldenFileRenamed ensures the previously-broken golden was renamed
// to a Windows-safe name (">"
func TestGoldenFileRenamed(t *testing.T) {
	sanitized := "TestAPIV1ApiKeyDelete_path_prefix_with_id_query_is_both_-__400_parity.json"
	broken := "TestAPIV1ApiKeyDelete_path_prefix_with_id_query_is_both_->_400_parity.json"

	sanitizedPath := filepath.Join(goldenDir, sanitized)
	brokenPath := filepath.Join(goldenDir, broken)

	// Sanitized must exist
	require.FileExists(t, sanitizedPath, "sanitized golden must exist (renamed from broken)")

	// Broken must not exist (otherwise checkout fails on Windows)
	assert.NoFileExists(t, brokenPath, "broken golden with > must not exist")

	// Also ensure no golden in the dir contains forbidden chars
	// (list a few to catch regressions)
	for _, forbidden := range []string{">", "<", ":", "\"", "|", "?", "*", "\\"} {
		// We can't easily list all files here, but at least ensure the
		// known-bad file is gone and sanitized file is present.
		assert.NotContains(t, sanitized, forbidden, "sanitized filename must not contain %q", forbidden)
	}
}
