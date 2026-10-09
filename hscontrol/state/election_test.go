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
}
