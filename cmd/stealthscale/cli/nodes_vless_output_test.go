package cli

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tomiwebpro/stealthscale/hscontrol/xray"
)

// captureStreams runs fn with os.Stdout and os.Stderr swapped for
// pipes and returns whatever was written to each.
func captureStreams(t *testing.T, fn func()) (string, string) {
	t.Helper()

	oldStdout, oldStderr := os.Stdout, os.Stderr

	rOut, wOut, err := os.Pipe()
	require.NoError(t, err)

	rErr, wErr, err := os.Pipe()
	require.NoError(t, err)

	os.Stdout, os.Stderr = wOut, wErr

	defer func() {
		os.Stdout, os.Stderr = oldStdout, oldStderr
	}()

	fn()

	require.NoError(t, wOut.Close())
	require.NoError(t, wErr.Close())

	out, err := io.ReadAll(rOut)
	require.NoError(t, err)

	errOut, err := io.ReadAll(rErr)
	require.NoError(t, err)

	return string(out), string(errOut)
}

// setVLESSConfig points viper at a deterministic xray config and
// restores the previous values when the test ends. xray.secret is
// left unset: with no sqlite path the identity initialiser falls
// back to an in-memory ephemeral secret, which is all the URI
// shape assertions need.
func setVLESSConfig(t *testing.T) {
	t.Helper()

	keys := map[string]string{
		"database.type":         "sqlite3",
		"xray.listen_addr":      "0.0.0.0",
		"xray.listen_port":      "10001",
		"xray.max_listen_port":  "10100",
		"xray.security":         "reality_xtls",
		"xray.timeout":          "30s",
		"xray.utls_fingerprint": "chrome",
		"xray.reality.dest":     "www.cloudflare.com:443",
	}

	for k, v := range keys {
		old := viper.Get(k)

		viper.Set(k, v)

		t.Cleanup(func() {
			viper.Set(k, old)
		})
	}
}

// TestNodeVLESSOneLineStdoutIsOnlyURI pins the pipe contract of
// `stscale nodes vless <id> --one-line`: with the default
// xray.listen_addr (0.0.0.0), stdout must contain ONLY the
// vless:// URI. The non-dialable-address warning must go to
// stderr, because the documented one-command join captures
// stdout verbatim:
//
//	stscale up --vless-uri "$(stscale nodes vless 1 --one-line)"
//
// Regression: the warning used to print to stdout via pterm's
// default writer, so the command substitution captured a
// two-line string and xray.ParseVLESSURI rejected it.
func TestNodeVLESSOneLineStdoutIsOnlyURI(t *testing.T) {
	setVLESSConfig(t)

	require.NoError(t, nodeVLESSCommand.Flags().Set("one-line", "true"))

	t.Cleanup(func() {
		require.NoError(t, nodeVLESSCommand.Flags().Set("one-line", "false"))
	})

	stdout, stderr := captureStreams(t, func() {
		require.NoError(t, nodeVLESSCommand.RunE(nodeVLESSCommand, []string{"1"}))
	})

	stdout = strings.TrimSpace(stdout)

	assert.True(t, strings.HasPrefix(stdout, "vless://"),
		"stdout must be only the vless URI, got %q", stdout)

	assert.NotContains(t, stdout, "URI contains",
		"warning must not pollute stdout")

	assert.NotContains(t, stdout, "WARNING",
		"warning prefix must not pollute stdout")

	assert.Contains(t, stderr, "URI contains",
		"warning must go to stderr")

	assert.Contains(t, stderr, "0.0.0.0",
		"warning must name the non-dialable address")
}

// TestNodeVLESSOneLineURIParsesRoundTrips verifies the URI captured
// from --one-line output survives xray.ParseVLESSURI — the second
// half of the one-command join contract.
func TestNodeVLESSOneLineURIParsesRoundTrips(t *testing.T) {
	setVLESSConfig(t)

	require.NoError(t, nodeVLESSCommand.Flags().Set("one-line", "true"))

	t.Cleanup(func() {
		require.NoError(t, nodeVLESSCommand.Flags().Set("one-line", "false"))
	})

	stdout, _ := captureStreams(t, func() {
		require.NoError(t, nodeVLESSCommand.RunE(nodeVLESSCommand, []string{"1"}))
	})

	uri := strings.TrimSpace(stdout)

	cfg, err := xray.ParseVLESSURI(uri)
	require.NoError(t, err, "captured URI must parse: %q", uri)

	assert.NotEmpty(t, cfg.ID)
	assert.Equal(t, "0.0.0.0", cfg.Address)
	assert.Positive(t, cfg.Port)
}
