// Copyright (c) 2026 TomiWebPro <TomiWebPro@gmail.com>
// SPDX-License-Identifier: BSD-3-Clause

package state

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
	"github.com/tomiwebpro/stealthscale/hscontrol/xray"
)

// errTestRebindFailed is returned by the failing
// change listener in TestSetNodeVLESSOverrideStore.
var errTestRebindFailed = errors.New("rebind failed")

// TestSetNodeVLESSOverrideStore pins the per-node VLESS
// endpoint override store (Web UI runtime apply, goal #4):
// overrides are stored and read back as copies, validated
// against the transport rules, cleared by a nil set, and
// change listeners fire on every store and clear.
func TestSetNodeVLESSOverrideStore(t *testing.T) {
	dbPath := t.TempDir() + "/vless-override.db"
	cfg := persistTestConfig(dbPath)

	s, err := NewState(cfg)
	require.NoError(t, err)

	t.Cleanup(func() { _ = s.Close() })

	nodeID := types.NodeID(1)

	// Without an override the node uses its derived endpoint.
	assert.Nil(t, s.NodeVLESS(nodeID))

	endpoint := &xray.VLESSConfig{ //nolint:exhaustruct_v5 // test fixture: only fields under test
		ID:       xray.NodeUUID(nodeID, "test-secret"),
		Network:  "tcp",
		Address:  "10.0.0.1",
		Port:     20001,
		Security: "reality_xtls",
	}
	require.NoError(t, s.SetNodeVLESS(nodeID, endpoint))

	got := s.NodeVLESS(nodeID)
	require.NotNil(t, got)
	assert.Equal(t, "10.0.0.1", got.Address)
	assert.Equal(t, 20001, got.Port)
	assert.Equal(t, "reality_xtls", got.Security)

	// The stored override is a copy: mutating the caller's
	// struct or the returned one must not change state.
	endpoint.Port = 20002
	got.Port = 20003
	assert.Equal(t, 20001, s.NodeVLESS(nodeID).Port)

	// Settings follow the transport rules: an invalid
	// override is rejected and leaves the stored one in place.
	invalid := func(endpoint *xray.VLESSConfig) {
		t.Helper()

		require.Error(t, s.SetNodeVLESS(nodeID, endpoint))
		assert.Equal(t, 20001, s.NodeVLESS(nodeID).Port)
	}
	invalid(&xray.VLESSConfig{ID: "uuid", Address: "10.0.0.1", Port: 65536, Security: "none"}) //nolint:exhaustruct_v5 // test fixture: only fields under test
	// Empty address.
	invalid(&xray.VLESSConfig{ID: "uuid", Port: 20001, Security: "none"}) //nolint:exhaustruct_v5 // test fixture: only fields under test
	// Unknown security variant.
	invalid(&xray.VLESSConfig{ //nolint:exhaustruct_v5 // test fixture: only fields under test
		ID:       "uuid",
		Address:  "10.0.0.1",
		Port:     20001,
		Security: "carrier-pigeon",
	})
	// Empty uuid.
	invalid(&xray.VLESSConfig{Address: "10.0.0.1", Port: 20001, Security: "none"}) //nolint:exhaustruct_v5 // test fixture: only fields under test

	// A zero node id is rejected.
	require.Error(t, s.SetNodeVLESS(0, endpoint))

	// Change listeners fire on every store and clear, with
	// the affected node ID.
	var fired []types.NodeID

	s.OnNodeVLESSChange(func(id types.NodeID) error {
		fired = append(fired, id)
		return nil
	})

	require.NoError(t, s.SetNodeVLESS(nodeID, endpoint))
	require.Equal(t, []types.NodeID{nodeID}, fired)

	// A nil override clears the store and fires the
	// listeners again.
	require.NoError(t, s.SetNodeVLESS(nodeID, nil))
	assert.Nil(t, s.NodeVLESS(nodeID))
	require.Equal(t, []types.NodeID{nodeID, nodeID}, fired)

	// A listener error is returned to the applier.
	s.OnNodeVLESSChange(func(types.NodeID) error {
		return errTestRebindFailed
	})

	err = s.SetNodeVLESS(nodeID, endpoint)
	require.Error(t, err)
	assert.ErrorContains(t, err, "rebind failed")
}
