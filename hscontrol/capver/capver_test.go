package capver

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"tailscale.com/tailcfg"
)

func TestTailscaleLatestMajorMinor(t *testing.T) {
	for _, test := range tailscaleLatestMajorMinorTests {
		t.Run("", func(t *testing.T) {
			output := TailscaleLatestMajorMinor(test.n, test.stripV)
			if diff := cmp.Diff(output, test.expected); diff != "" {
				t.Errorf("TailscaleLatestMajorMinor(%d, %v) mismatch (-want +got):\n%s", test.n, test.stripV, diff)
			}
		})
	}
}

func TestCapVerMinimumTailscaleVersion(t *testing.T) {
	for _, test := range capVerMinimumTailscaleVersionTests {
		t.Run("", func(t *testing.T) {
			output := TailscaleVersion(test.input)
			if output != test.expected {
				t.Errorf("CapVerFromTailscaleVersion(%d) = %s; want %s", test.input, output, test.expected)
			}
		})
	}
}

// TestCapabilityVersion pins the Tailscale-version-string to
// capability-version mapping, including the contract that an
// unknown NEWER version resolves to the current capability
// version rather than 0: a client newer than this server's
// version map must not read as unsupported (isSupportedVersion(0)
// is false, so a 0 would have /key and the Noise handshake
// reject a perfectly good newer client).
func TestCapabilityVersion(t *testing.T) {
	tests := []struct {
		input    string
		expected tailcfg.CapabilityVersion
	}{
		// Known minors: direct lookup.
		{"v1.24", 32},
		{"v1.90", 130},
		{"v1.101", 141},
		// Full versions resolve through their minor.
		{"v1.90.1", 130},
		{"v1.101.7", 141},
		// The v prefix is optional.
		{"1.90", 130},
		{"1.90.1", 130},
		// Unknown NEWER versions map to the current capability
		// version, whether the major or the minor is newer.
		{"v1.999", tailcfg.CurrentCapabilityVersion},
		{"v1.999.5", tailcfg.CurrentCapabilityVersion},
		{"v2.0", tailcfg.CurrentCapabilityVersion},
		// Unknown OLDER versions and malformed input map to 0.
		{"v1.20", 0},
		{"v1.20.3", 0},
		{"v1.abc", 0},
		{"v1", 0},
		{"", 0},
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			if got := CapabilityVersion(test.input); got != test.expected {
				t.Errorf("CapabilityVersion(%q) = %d; want %d", test.input, got, test.expected)
			}
		})
	}
}

// TestCapabilityVersionNewerClientNegotiation pins the
// end-to-end version-negotiation contract for a client newer
// than the server's version map: its version string resolves to
// the server's current capability version, which is supported
// and carries the label of the latest known minor.
func TestCapabilityVersionNewerClientNegotiation(t *testing.T) {
	cv := CapabilityVersion("v1.999")
	if cv != tailcfg.CurrentCapabilityVersion {
		t.Fatalf("CapabilityVersion(%q) = %d; want %d", "v1.999", cv, tailcfg.CurrentCapabilityVersion)
	}

	label := TailscaleVersion(cv)
	if label == "" {
		t.Fatalf("TailscaleVersion(%d) is empty; want the latest known minor label", cv)
	}

	if want := TailscaleVersion(tailcfg.CurrentCapabilityVersion); label != want {
		t.Errorf("TailscaleVersion(%d) = %q; want %q", cv, label, want)
	}
}
