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

// TestSetNodeVLESSRollsBackWhenRebindFails pins the
// atomicity of the override apply: the override is
// stored before the change listeners fire, so a failed
// rebind must roll the store back to the previous
// override and fire the listeners again — the node must
// never keep an override whose endpoint nothing serves.
func TestSetNodeVLESSRollsBackWhenRebindFails(t *testing.T) {
	dbPath := t.TempDir() + "/vless-rollback.db"
	cfg := persistTestConfig(dbPath)

	s, err := NewState(cfg)
	require.NoError(t, err)

	t.Cleanup(func() { _ = s.Close() })

	nodeID := types.NodeID(2)

	first := &xray.VLESSConfig{ //nolint:exhaustruct_v5 // test fixture: only fields under test
		ID:       xray.NodeUUID(nodeID, "test-secret"),
		Network:  "tcp",
		Address:  "10.0.0.1",
		Port:     20010,
		Security: "reality_xtls",
	}
	second := &xray.VLESSConfig{ //nolint:exhaustruct_v5 // test fixture: only fields under test
		ID:       xray.NodeUUID(nodeID, "test-secret"),
		Network:  "tcp",
		Address:  "10.0.0.2",
		Port:     20011,
		Security: "reality_xtls",
	}

	// The listener records the override the store held
	// when it fired: the apply, the failed re-apply and
	// the rollback re-fire.
	var seenPorts []int

	failNext := false

	s.OnNodeVLESSChange(func(id types.NodeID) error {
		ov := s.NodeVLESS(id)
		if ov == nil {
			seenPorts = append(seenPorts, 0)
		} else {
			seenPorts = append(seenPorts, ov.Port)
		}

		if failNext {
			failNext = false
			return errTestRebindFailed
		}

		return nil
	})

	require.NoError(t, s.SetNodeVLESS(nodeID, first))
	require.Equal(t, []int{first.Port}, seenPorts)

	failNext = true

	err = s.SetNodeVLESS(nodeID, second)
	require.Error(t, err, "a failed rebind must fail the apply")
	require.ErrorContains(t, err, "rebind failed")

	// The store was rolled back to the previous
	// override...
	got := s.NodeVLESS(nodeID)
	require.NotNil(t, got)
	assert.Equal(t, first.Port, got.Port)
	assert.Equal(t, first.Address, got.Address)

	// ...and the listeners fired again with the rolled
	// back override (apply, failed apply, rollback).
	require.Equal(t, []int{first.Port, second.Port, first.Port}, seenPorts)
}

// TestSetNodeVLESSRollsBackToClearWhenRebindFails pins
// the rollback when no previous override exists: a failed
// first apply leaves the node on its config-derived
// endpoint — the store stays empty instead of keeping an
// override whose rebind failed.
func TestSetNodeVLESSRollsBackToClearWhenRebindFails(t *testing.T) {
	dbPath := t.TempDir() + "/vless-rollback-clear.db"
	cfg := persistTestConfig(dbPath)

	s, err := NewState(cfg)
	require.NoError(t, err)

	t.Cleanup(func() { _ = s.Close() })

	nodeID := types.NodeID(3)

	s.OnNodeVLESSChange(func(types.NodeID) error {
		return errTestRebindFailed
	})

	endpoint := &xray.VLESSConfig{ //nolint:exhaustruct_v5 // test fixture: only fields under test
		ID:       xray.NodeUUID(nodeID, "test-secret"),
		Network:  "tcp",
		Address:  "10.0.0.1",
		Port:     20012,
		Security: "reality_xtls",
	}

	err = s.SetNodeVLESS(nodeID, endpoint)
	require.Error(t, err)
	assert.Nil(t, s.NodeVLESS(nodeID), "a failed first apply must leave the node on its config-derived endpoint")
}
