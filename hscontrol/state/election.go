package state

import (
	"sync"

	"github.com/tomiwebpro/stealthscale/hscontrol/types"
)

// ElectionMode describes how coordinator selection works. The
// canonical definition lives in hscontrol/types so the WebUI
// write delegation — which must not import this package — can
// name the type in its type-asserted write interface.
type ElectionMode = types.ElectionMode

const (
	ElectionModeAuto    = types.ElectionModeAuto
	ElectionModeManual  = types.ElectionModeManual
	ElectionModeUnknown = types.ElectionModeUnknown
)

// ElectionConfig holds the coordination-election knobs.
type ElectionConfig struct {
	Mode          ElectionMode `json:"mode"`
	PrimaryNodeID uint64       `json:"primary_node_id,omitempty"` // used when Mode == manual
}

// ElectionState carries the live view of coordinator election.
type ElectionState struct {
	mu      sync.RWMutex
	mode    ElectionMode
	primary types.NodeID // zero means none elected
}

// NewElectionState creates an election state initialised to automatic mode.
func NewElectionState() *ElectionState {
	return &ElectionState{mode: ElectionModeAuto}
}

// Mode returns the current election mode.
func (e *ElectionState) Mode() ElectionMode {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.mode
}

// SetMode changes the election mode. Returns the previous mode.
// Switching to auto clears any fixed primary: in auto mode any
// node may become coordinator, so a stored primary would be
// stale.
func (e *ElectionState) SetMode(mode ElectionMode) ElectionMode {
	e.mu.Lock()
	defer e.mu.Unlock()
	prev := e.mode
	e.mode = mode
	if mode == ElectionModeAuto {
		e.primary = 0
	}
	return prev
}

// Primary returns the current elected primary coordinator node ID and whether one is set.
func (e *ElectionState) Primary() (types.NodeID, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.primary, e.primary != 0
}

// SetPrimary sets the elected primary. Pass a zero NodeID to clear it.
func (e *ElectionState) SetPrimary(id types.NodeID) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.primary = id
}

// Snapshot returns a serialisable copy of the election state.
func (e *ElectionState) Snapshot() map[string]any {
	e.mu.RLock()
	defer e.mu.RUnlock()
	primary := ""
	if e.primary != 0 {
		primary = e.primary.String()
	}
	return map[string]any{
		"mode":         string(e.mode),
		"primary_node": primary,
		"has_primary":  e.primary != 0,
	}
}
