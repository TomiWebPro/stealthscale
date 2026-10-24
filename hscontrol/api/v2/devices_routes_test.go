package apiv2

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"tailscale.com/net/tsaddr"
)

// TestParseRoutesValidation exercises every failure mode of parseRoutes:
// malformed prefixes, empty arrays, and the bidirectional expansion path
// that turns a single-family "all" into both IPv4+IPv6.
func TestParseRoutesValidation(t *testing.T) {
	t.Parallel()

	t.Run("rejects_malformed_prefix", func(t *testing.T) {
		t.Parallel()

		_, err := parseRoutes([]string{"not-a-prefix"})
		assert.Error(t, err, "malformed prefix must be rejected")
	})

	t.Run("rejects_non_cidr", func(t *testing.T) {
		t.Parallel()

		_, err := parseRoutes([]string{"192.168.1.1"})
		assert.Error(t, err, "bare IP without /mask must be rejected")
	})

	t.Run("empty_array_returns_empty", func(t *testing.T) {
		t.Parallel()

		routes, err := parseRoutes(nil)
		require.NoError(t, err)
		assert.Empty(t, routes, "nil routes must produce an empty slice")

		routes, err = parseRoutes([]string{})
		require.NoError(t, err)
		assert.Empty(t, routes, "empty slice routes must produce an empty slice")
	})

	t.Run("single_valid_prefix_returns_sorted_deduped", func(t *testing.T) {
		t.Parallel()

		routes, err := parseRoutes([]string{"10.0.0.0/24"})
		require.NoError(t, err)
		require.Len(t, routes, 1)
		assert.Equal(t, netip.MustParsePrefix("10.0.0.0/24"), routes[0])
	})

	t.Run("duplicates_removed_after_sort", func(t *testing.T) {
		t.Parallel()

		// Two identical prefixes should collapse to one.
		routes, err := parseRoutes([]string{"10.0.0.0/24", "10.0.0.0/24"})
		require.NoError(t, err)
		require.Len(t, routes, 1)
		assert.Equal(t, netip.MustParsePrefix("10.0.0.0/24"), routes[0])
	})

	t.Run("mixed_families_sorted_by_compare", func(t *testing.T) {
		t.Parallel()

		routes, err := parseRoutes([]string{
			"fe80::/10",
			"10.0.0.0/8",
			"fd7a::/48",
		})
		require.NoError(t, err)
		require.Len(t, routes, 3)
		// Verify sorting: IPv4 always sorts before IPv6 (Compare uses
		// isV6 flag first); among IPv6, lexicographic byte order applies.
		assert.Equal(t, netip.MustParsePrefix("10.0.0.0/8"), routes[0])
		assert.Equal(t, netip.MustParsePrefix("fd7a::/48"), routes[1])
		assert.Equal(t, netip.MustParsePrefix("fe80::/10"), routes[2])
	})

	t.Run("expand_ipv4_all_to_both_families", func(t *testing.T) {
		t.Parallel()

		// "100.64.0.0/10" (tsaddr.AllIPv4()) should expand to
		// both IPv4 all + IPv6 ULA all — matching the exit-node
		// convention where enabling "all" routes means both families.
		routes, err := parseRoutes([]string{tsaddr.AllIPv4().String()})
		require.NoError(t, err)
		require.Len(t, routes, 2)

		var foundV4 bool

		var foundV6 bool

		for _, r := range routes {
			if r == tsaddr.AllIPv4() {
				foundV4 = true
			}

			if r == tsaddr.AllIPv6() {
				foundV6 = true
			}
		}

		assert.True(t, foundV4, "must contain IPv4 all")

		assert.True(t, foundV6, "must contain IPv6 all")
	})

	t.Run("expand_ipv6_all_to_both_families", func(t *testing.T) {
		t.Parallel()

		routes, err := parseRoutes([]string{tsaddr.AllIPv6().String()})
		require.NoError(t, err)
		require.Len(t, routes, 2)

		var foundV4 bool

		var foundV6 bool

		for _, r := range routes {
			if r == tsaddr.AllIPv4() {
				foundV4 = true
			}

			if r == tsaddr.AllIPv6() {
				foundV6 = true
			}
		}

		assert.True(t, foundV4, "must contain IPv4 all")

		assert.True(t, foundV6, "must contain IPv6 all")
	})

	t.Run("all_and_specific_still_expand_correctly", func(t *testing.T) {
		t.Parallel()

		// If someone sends both "all" and a specific route, all should
		// expand and the specific route should remain (but no dupes from
		// the expansion itself).
		routes, err := parseRoutes([]string{
			tsaddr.AllIPv4().String(),
			"10.0.0.0/24",
		})
		require.NoError(t, err)
		// "all" expands to V4+V6, plus the specific route = 3 total.
		require.Len(t, routes, 3)

		var hasAllV4 bool

		var hasAllV6 bool

		for _, r := range routes {
			if r == tsaddr.AllIPv4() {
				hasAllV4 = true
			}

			if r == tsaddr.AllIPv6() {
				hasAllV6 = true
			}
		}

		assert.True(t, hasAllV4)

		assert.True(t, hasAllV6)
	})
}
