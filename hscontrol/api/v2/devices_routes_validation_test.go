package apiv2

import (
	"net/netip"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	"tailscale.com/net/tsaddr"
)

// TestParseRoutesDedupAfterExpansion verifies that when both an "all" prefix
// (which expands to V4+V6 — see parseRoutes line 396) and explicit member
// prefixes are provided, the deduplication produces exactly the expected set.
func TestParseRoutesDedupAfterExpansion(t *testing.T) {
	t.Parallel()

	// tsaddr.AllIPv4() is "0.0.0.0/0", so providing that + a specific route
	// must produce AllIPv4+AllIPv6 + the specific route = 3 routes.
	routes, err := parseRoutes([]string{"0.0.0.0/0", "100.64.0.1/32"})
	require.NoError(t, err)

	requireLen := func(n int) {
		t.Helper()

		if len(routes) != n {
			t.Fatalf("expected %d routes, got %d", n, len(routes))
		}
	}
	requireLen(3)

	// Must contain AllIPv4, AllIPv6, and the specific /32.
	expected := []netip.Prefix{
		tsaddr.AllIPv4(),
		tsaddr.AllIPv6(),
		netip.MustParsePrefix("100.64.0.1/32"),
	}
	for _, want := range expected {
		require.True(t, slices.Contains(routes, want), "missing expected route %v", want)
	}
}
