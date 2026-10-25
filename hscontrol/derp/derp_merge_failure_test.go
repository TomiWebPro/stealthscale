// Copyright (c) 2026 TomiWebPro <TomiWebPro@gmail.com>
// SPDX-License-Identifier: BSD-3-Clause

package derp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"tailscale.com/tailcfg"
)

// TestMergeDERPMapsHandlesNilEntries pins the nil-entry guard:
// when a caller passes nil pointers in the derpMaps slice (for example,
// after failing to load one source while succeeding with another),
// mergeDERPMaps must skip the nil rather than panicking — so a partial
// config load doesn't crash the DERP subsystem.
func TestMergeDERPMapsHandlesNilEntries(t *testing.T) {
	t.Parallel()

	src := &tailcfg.DERPMap{
		Regions: map[int]*tailcfg.DERPRegion{
			1: {RegionID: 1, Nodes: []*tailcfg.DERPNode{{Name: "a"}}},
		},
	}

	merged := mergeDERPMaps([]*tailcfg.DERPMap{src, nil, nil})

	require.NotNil(t, merged)
	require.Len(t, merged.Regions, 1)
	assert.Equal(t, src.Regions[1].Nodes[0].Name, merged.Regions[1].Nodes[0].Name)
}

// TestMergeDERPMapsEmptyRegionsIsNoOp verifies that a zero-value DERP map
// (nil Regions, which can appear when a YAML file is valid but contains no
// region blocks) does not panic and contributes nothing to the result.
func TestMergeDERPMapsEmptyRegionsIsNoOp(t *testing.T) {
	t.Parallel()

	src := &tailcfg.DERPMap{
		Regions: map[int]*tailcfg.DERPRegion{
			5: {RegionID: 5, RegionCode: "nrt"},
		},
	}

	merged := mergeDERPMaps([]*tailcfg.DERPMap{src, {}, nil})

	require.NotNil(t, merged)
	require.Len(t, merged.Regions, 1)
	assert.Equal(t, "nrt", merged.Regions[5].RegionCode)
}

// TestMergeDERPMapsLastWins confirms the overlay semantics:
// when two DERP maps define the same region ID, the one appearing later
// in the list completely replaces the earlier one (including all nodes),
// rather than merging node lists. This ensures a later URL/path takes
// authoritative precedence.
func TestMergeDERPMapsLastWins(t *testing.T) {
	t.Parallel()

	early := &tailcfg.DERPMap{
		Regions: map[int]*tailcfg.DERPRegion{
			42: {RegionID: 42, RegionCode: "old", Nodes: []*tailcfg.DERPNode{{Name: "x"}}},
		},
	}
	later := &tailcfg.DERPMap{
		Regions: map[int]*tailcfg.DERPRegion{
			42: {RegionID: 42, RegionCode: "new", Nodes: []*tailcfg.DERPNode{{Name: "y"}}},
		},
	}

	merged := mergeDERPMaps([]*tailcfg.DERPMap{early, later})

	require.Len(t, merged.Regions, 1)
	assert.Equal(t, "new", merged.Regions[42].RegionCode, "later map's region must win")
	assert.Equal(t, []string{"y"}, regionNames(merged.Regions[42]))
}

// TestMergeDERPMapsPreservesUnaffectedRegions verifies that a late map
// with overlapping IDs only touches those IDs; other regions from early
// maps survive untouched.
func TestMergeDERPMapsPreservesUnaffectedRegions(t *testing.T) {
	t.Parallel()

	first := &tailcfg.DERPMap{
		Regions: map[int]*tailcfg.DERPRegion{
			1: {RegionID: 1, RegionCode: "nyc"},
			2: {RegionID: 2, RegionCode: "sfo"},
			3: {RegionID: 3, RegionCode: "fra"},
		},
	}
	second := &tailcfg.DERPMap{
		Regions: map[int]*tailcfg.DERPRegion{
			2: {RegionID: 2, RegionCode: "sfo-v2"},
		},
	}

	merged := mergeDERPMaps([]*tailcfg.DERPMap{first, second})

	require.Len(t, merged.Regions, 3, "all three regions must survive")
	assert.Equal(t, "nyc", merged.Regions[1].RegionCode)
	assert.Equal(t, "sfo-v2", merged.Regions[2].RegionCode)
	assert.Equal(t, "fra", merged.Regions[3].RegionCode)
}

// TestMergeDERPMapsReturnsNonNilAlways pins the invariant that the return
// value is never nil: even with an empty input slice, callers (the ticker
// goroutine, MapRequest handlers) expect a non-nil DERPMap to read from
// without nil-checking.
func TestMergeDERPMapsReturnsNonNilAlways(t *testing.T) {
	t.Parallel()

	got := mergeDERPMaps(nil)
	require.NotNil(t, got)

	got = mergeDERPMaps([]*tailcfg.DERPMap{}) // no elements
	require.NotNil(t, got)
}

// regionNames returns a slice of node names for convenience in tests.
func regionNames(region *tailcfg.DERPRegion) []string {
	names := make([]string, len(region.Nodes))
	for i, n := range region.Nodes {
		names[i] = n.Name
	}
	return names
}
