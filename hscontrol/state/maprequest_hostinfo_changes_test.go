package state

import (
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
	"gorm.io/gorm"
	"tailscale.com/tailcfg"
)

// TestMapRequestHostinfoHostnameChangeTriggersPersist verifies that a
// MapRequest whose only difference from the stored state is the hostname
// does hit the database (and triggers the O(n) policy rescan). This is
// important because clients frequently send keepalives that include small
// hostinfo drift (hostname refreshes, OS bumps).
func TestMapRequestHostinfoHostnameChangeTriggersPersist(t *testing.T) {
	t.Parallel()

	_, s, nodeID := persistTestSetup(t)
	t.Cleanup(func() { _ = s.Close() })

	var nodeUpdateCount atomic.Int64

	gdb := s.DB().DB
	cbName := "hnc_count_node_updates"
	err := gdb.Callback().Update().After("gorm:update").Register(cbName, func(tx *gorm.DB) {
		if tx.Statement == nil {
			return
		}

		if tx.Statement.Table == "nodes" ||
			strings.Contains(strings.ToLower(tx.Statement.SQL.String()), "update \"nodes\"") {
			nodeUpdateCount.Add(1)
		}
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = gdb.Callback().Update().Remove(cbName) })

	nv, ok := s.GetNodeByID(nodeID)
	require.True(t, ok, "node should exist in NodeStore")

	stored := nv.AsStruct()

	// First request establishes baseline Hostinfo/DERP (also persists).
	reqInit := tailcfg.MapRequest{ //nolint:exhaustruct_v5 // test fixture
		NodeKey:  stored.NodeKey,
		DiscoKey: stored.DiscoKey,
		Hostinfo: &tailcfg.Hostinfo{ //nolint:exhaustruct_v5 // test fixture
			Hostname: stored.Hostname,
			NetInfo:  &tailcfg.NetInfo{PreferredDERP: 5}, //nolint:exhaustruct_v5 // test fixture
		},
	}
	_, err = s.UpdateNodeFromMapRequest(nodeID, reqInit)
	require.NoError(t, err)

	nodeUpdateCount.Store(0)

	// Update request: differ ONLY in hostname → must persist.
	req := tailcfg.MapRequest{ //nolint:exhaustruct_v5 // test fixture
		NodeKey:  stored.NodeKey,
		DiscoKey: stored.DiscoKey,
		Hostinfo: &tailcfg.Hostinfo{ //nolint:exhaustruct_v5 // test fixture
			Hostname: "changed-hostname",
			NetInfo:  &tailcfg.NetInfo{PreferredDERP: 5}, //nolint:exhaustruct_v5 // test fixture
		},
	}

	_, err = s.UpdateNodeFromMapRequest(nodeID, req)
	require.NoError(t, err, "MapRequest with hostname change must not error")

	assert.GreaterOrEqual(t, nodeUpdateCount.Load(), int64(1),
		"hostname-only change must trigger at least one nodes-table UPDATE")
}

// TestMapRequestStaleKeepaliveDoesNotPersist verifies that a keepalive
// MapRequest carrying no substantive change (hostname, endpoints, keys,
// DERP region identical to stored) bypasses the database UPDATE and the
// O(n) policy scan that follows. Stale keepalives arrive every few
// seconds under load; hitting the DB per-request creates measurable waste.
func TestMapRequestStaleKeepaliveDoesNotPersist(t *testing.T) {
	t.Parallel()

	_, s, nodeID := persistTestSetup(t)
	t.Cleanup(func() { _ = s.Close() })

	var nodeUpdateCount atomic.Int64

	gdb := s.DB().DB
	cbName := "sk_count_node_updates"
	err := gdb.Callback().Update().After("gorm:update").Register(cbName, func(tx *gorm.DB) {
		if tx.Statement == nil {
			return
		}

		if tx.Statement.Table == "nodes" ||
			strings.Contains(strings.ToLower(tx.Statement.SQL.String()), "update \"nodes\"") {
			nodeUpdateCount.Add(1)
		}
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = gdb.Callback().Update().Remove(cbName) })

	nv, ok := s.GetNodeByID(nodeID)
	require.True(t, ok)

	stored := nv.AsStruct()

	// Establish baseline state.
	reqEstablish := tailcfg.MapRequest{ //nolint:exhaustruct_v5 // test fixture
		NodeKey:  stored.NodeKey,
		DiscoKey: stored.DiscoKey,
		Hostinfo: &tailcfg.Hostinfo{ //nolint:exhaustruct_v5 // test fixture
			Hostname: stored.Hostname,
			NetInfo:  &tailcfg.NetInfo{PreferredDERP: 3}, //nolint:exhaustruct_v5 // test fixture
		},
	}
	_, err = s.UpdateNodeFromMapRequest(nodeID, reqEstablish)
	require.NoError(t, err)

	nodeUpdateCount.Store(0)

	// Identical re-send — true no-op: no DB write expected.
	req := tailcfg.MapRequest{ //nolint:exhaustruct_v5 // test fixture
		NodeKey:  stored.NodeKey,
		DiscoKey: stored.DiscoKey,
		Hostinfo: &tailcfg.Hostinfo{ //nolint:exhaustruct_v5 // test fixture
			Hostname: stored.Hostname,
			NetInfo:  &tailcfg.NetInfo{PreferredDERP: 3}, //nolint:exhaustruct_v5 // test fixture
		},
	}

	_, err = s.UpdateNodeFromMapRequest(nodeID, req)
	require.NoError(t, err)

	assert.Equal(t, int64(0), nodeUpdateCount.Load(),
		"stale keepalive with unchanged state must not issue any nodes-table UPDATE")
}

// TestHostinfoEqualWithNilFields pins the contract of [hostinfoEqual]
// across the null-pointer combinations that gate [persistWorthy]. The
// function feeds directly into whether the critical map-request hot
// path emits a database write, so these paths must never panic or
// mis-classify.
func TestHostinfoEqualWithNilFields(t *testing.T) {
	t.Parallel()

	// Use a real node from persistTestSetup to get an authentic Hostinfo
	// blob that survives through State processing. First establish
	// baseline state so the node actually has a Hostinfo blob we can
	// compare against.
	_, s, nodeID := persistTestSetup(t)
	t.Cleanup(func() { _ = s.Close() })

	nv, ok := s.GetNodeByID(nodeID)
	require.True(t, ok)

	dummyReq := tailcfg.MapRequest{ //nolint:exhaustruct_v5 // test fixture
		NodeKey:  nv.NodeKey(),
		DiscoKey: nv.DiscoKey(),
		Hostinfo: &tailcfg.Hostinfo{ //nolint:exhaustruct_v5 // test fixture
			Hostname: "test-node",
			NetInfo:  &tailcfg.NetInfo{PreferredDERP: 5}, //nolint:exhaustruct_v5 // test fixture
		},
	}
	_, err := s.UpdateNodeFromMapRequest(nodeID, dummyReq)
	require.NoError(t, err)

	oldNode, ok := s.GetNodeByID(nodeID)
	require.True(t, ok)

	tests := []struct {
		name   string
		old    types.NodeView
		newHI  *tailcfg.Hostinfo
		expect bool
	}{
		{
			name:   "invalid_node_with_nil_hostinfo_returns_true",
			old:    types.NodeView{}, // invalid / zero value
			newHI:  nil,
			expect: true,
		},
		{
			name:   "invalid_node_with_valid_hostinfo_returns_false",
			old:    types.NodeView{},                 // invalid
			newHI:  &tailcfg.Hostinfo{Hostname: "x"}, //nolint:exhaustruct_v5 // test fixture
			expect: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := hostinfoEqual(tc.old, tc.newHI)
			assert.Equal(t, tc.expect, got)
		})
	}

	// Now also exercise the (valid, nil) and (valid, valid) quadrants.
	assert.False(t, hostinfoEqual(oldNode, nil),
		"valid node with nil hostinfo must return false")

	// A Hostinfo with matching hostname AND NetInfo DERp → equal.
	hostMatch := &tailcfg.Hostinfo{ //nolint:exhaustruct_v5 // test fixture
		Hostname: oldNode.Hostname(),
		NetInfo:  &tailcfg.NetInfo{PreferredDERP: oldNode.Hostinfo().NetInfo().PreferredDERP()}, //nolint:exhaustruct_v5 // test fixture
	}
	assert.True(t, hostinfoEqual(oldNode, hostMatch),
		"matching hostname + NetInfo must be equal")

	// Different hostname → not equal.
	different := &tailcfg.Hostinfo{ //nolint:exhaustruct_v5 // test fixture
		Hostname: "totally-different",
	}
	assert.False(t, hostinfoEqual(oldNode, different))
}
