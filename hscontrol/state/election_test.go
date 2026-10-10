package state

import (
	"testing"

	"github.com/tomiwebpro/stealthscale/hscontrol/types"
)

func TestElectionState(t *testing.T) {
	e := NewElectionState()

	// Initial state is auto, no primary.
	if e.Mode() != ElectionModeAuto {
		t.Fatalf("mode = %s, want %s", e.Mode(), ElectionModeAuto)
	}
	if p, ok := e.Primary(); ok || p != 0 {
		t.Fatalf("Primary() = (%d, %v), want (0, false)", p, ok)
	}

	snap := e.Snapshot()
	if snap["mode"] != "auto" {
		t.Errorf("snapshot mode = %q, want %q", snap["mode"], "auto")
	}
	if snap["has_primary"] != false {
		t.Errorf("snapshot has_primary = %v, want false", snap["has_primary"])
	}

	// Set manual mode.
	prev := e.SetMode(ElectionModeManual)
	if prev != ElectionModeAuto {
		t.Fatalf("SetMode returned %s, want %s", prev, ElectionModeAuto)
	}
	if e.Mode() != ElectionModeManual {
		t.Fatalf("mode = %s, want %s", e.Mode(), ElectionModeManual)
	}

	// Set a primary node.
	testID := types.NodeID(42)
	e.SetPrimary(testID)
	p, ok := e.Primary()
	if !ok || p != testID {
		t.Fatalf("Primary() = (%d, %v), want (%d, true)", p, ok, testID)
	}

	snap = e.Snapshot()
	if snap["has_primary"] != true {
		t.Errorf("snapshot has_primary = %v, want true", snap["has_primary"])
	}
	if snap["primary_node"] != "42" {
		t.Errorf("snapshot primary_node = %q, want %q", snap["primary_node"], "42")
	}

	// Clear primary.
	e.SetPrimary(0)
	_, ok = e.Primary()
	if ok {
		t.Error("Primary after clear should return (0, false)")
	}

	// Switching back to auto clears any fixed primary:
	// in auto mode any node may become coordinator, so a
	// stored primary would be stale.
	e.SetPrimary(testID)
	prev = e.SetMode(ElectionModeAuto)
	if prev != ElectionModeManual {
		t.Fatalf("SetMode returned %s, want %s", prev, ElectionModeManual)
	}
	if p, ok := e.Primary(); ok || p != 0 {
		t.Fatalf("Primary after switching to auto = (%d, %v), want (0, false)", p, ok)
	}
	if snap := e.Snapshot(); snap["has_primary"] != false {
		t.Errorf("snapshot has_primary after auto switch = %v, want false", snap["has_primary"])
	}
}

// TestStateSetElectionMode pins the State-level election
// mode switch (goal #4: coordinator election must be
// configurable from the Web UI): a fresh State starts in
// auto mode, SetElectionMode switches to manual (where a
// fixed primary is meaningful) and back, and switching to
// auto clears the fixed primary.
func TestStateSetElectionMode(t *testing.T) {
	dbPath := t.TempDir() + "/election-state.db"
	cfg := persistTestConfig(dbPath)

	s, err := NewState(cfg)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if snap := s.ElectionSnapshot(); snap["mode"] != "auto" {
		t.Fatalf("initial mode = %v, want auto", snap["mode"])
	}

	s.SetElectionMode(ElectionModeManual)
	s.SetElectionPrimary(types.NodeID(42))

	snap := s.ElectionSnapshot()
	if snap["mode"] != "manual" {
		t.Fatalf("mode after SetElectionMode = %v, want manual", snap["mode"])
	}
	if snap["has_primary"] != true {
		t.Fatalf("has_primary = %v, want true", snap["has_primary"])
	}
	if snap["primary_node"] != "42" {
		t.Fatalf("primary_node = %v, want 42", snap["primary_node"])
	}

	// Switching to auto clears the fixed primary.
	s.SetElectionMode(ElectionModeAuto)
	snap = s.ElectionSnapshot()
	if snap["mode"] != "auto" {
		t.Fatalf("mode after switching back = %v, want auto", snap["mode"])
	}
	if snap["has_primary"] != false {
		t.Fatalf("has_primary after switching to auto = %v, want false", snap["has_primary"])
	}
}
