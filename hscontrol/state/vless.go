// Copyright (c) 2026 TomiWebPro <TomiWebPro@gmail.com>
// SPDX-License-Identifier: BSD-3-Clause

package state

import (
	"errors"
	"slices"

	"github.com/tomiwebpro/stealthscale/hscontrol/types"
	"github.com/tomiwebpro/stealthscale/hscontrol/xray"
)

// errInvalidNodeID rejects a zero node ID on the
// per-node VLESS endpoint override write path.
var errInvalidNodeID = errors.New("vless override: invalid node id")

// vlessAppliedState tracks, per node, the last
// successfully applied VLESS endpoint override and the
// sequence number of that apply. Applies to the same
// node may interleave (two API applies within the
// listener-firing window), so a failed apply rolls the
// store back to this state — never to the store's
// current entry, which may be a concurrent apply's
// not-yet-committed override whose own rebind has not
// settled yet.
type vlessAppliedState struct {
	last    *xray.VLESSConfig
	lastSeq uint64
}

// SetNodeVLESS stores an operator-applied per-node VLESS
// endpoint override (Web UI runtime apply, goal #4). The
// override is consulted by the VLESS read paths and enforced
// on the transport: the registered change listeners fire
// after the store so the node's VLESS listener rebinds to
// the overridden endpoint (a changed port moves the TCP
// listener; in-flight connections keep their deadline). A
// nil cfg clears the override, returning the node to its
// config-derived endpoint. Settings follow the transport's
// own rules (xray.VLESSConfig.Validate).
//
// The apply is atomic: the override is stored before the
// change listeners fire, so when a listener reports the
// rebind failed, the store is rolled back to the last
// successfully applied override (or its absence) and the
// listeners fire again — the node keeps serving, and every
// read path keeps reporting, the endpoint that was live
// before the apply. Applies to the same node may interleave:
// the rollback target is the per-node last-successful-apply
// state, not the store's current entry, so a failed apply
// neither resurrects a concurrent apply's failed override
// nor clobbers a concurrent apply that succeeded. The failed
// rebind's error (joined with any rollback rebind error) is
// returned to the applier.
func (s *State) SetNodeVLESS(nodeID types.NodeID, cfg *xray.VLESSConfig) error {
	if nodeID == 0 {
		return errInvalidNodeID
	}

	if cfg != nil {
		err := cfg.Validate()
		if err != nil {
			return err
		}
	}

	s.vlessMu.Lock()

	applied := s.vlessApplied[nodeID]
	if applied == nil {
		applied = &vlessAppliedState{} //nolint:exhaustruct_v5 // zero value: no successful apply yet
		s.vlessApplied[nodeID] = applied
	}

	// Sequence the apply so its commit or rollback can tell
	// whether a concurrent apply succeeded after this one
	// stored its override.
	seq := s.vlessSeq
	s.vlessSeq++

	var entry *xray.VLESSConfig

	if cfg == nil {
		delete(s.vlessOverrides, nodeID)
	} else {
		stored := *cfg
		entry = &stored
		s.vlessOverrides[nodeID] = entry
	}

	listeners := slices.Clone(s.vlessListeners)
	s.vlessMu.Unlock()

	errs := fireVLESSChangeListeners(listeners, nodeID)
	if len(errs) == 0 {
		return s.commitVLESSApply(nodeID, applied, entry, seq)
	}

	// A rebind failed: roll the store back to the last
	// successfully applied override and fire the listeners
	// again. The transport rebinds back to the endpoint that
	// was live before the apply; its running listener already
	// serves that endpoint (a failed rebind leaves it up), so
	// the re-fire is a no-op there.
	s.vlessMu.Lock()

	if applied.lastSeq > seq {
		// A concurrent apply succeeded after this apply stored
		// its override: that apply owns the node's state now,
		// and restoring this apply's previous state would
		// clobber it.
		s.vlessMu.Unlock()

		return errors.Join(errs...)
	}

	if applied.last != nil {
		s.vlessOverrides[nodeID] = applied.last
	} else {
		delete(s.vlessOverrides, nodeID)
	}

	listeners = slices.Clone(s.vlessListeners)
	s.vlessMu.Unlock()

	rollbackErrs := fireVLESSChangeListeners(listeners, nodeID)

	return errors.Join(append(errs, rollbackErrs...)...)
}

// commitVLESSApply records a successful apply: the stored
// override becomes the node's committed live state and the
// per-node last-successful-apply state advances to it.
// When a concurrent apply superseded the entry while the
// listeners fired, the entry is re-stored and the listeners
// re-fired so the read paths and the transport agree with
// this apply's success — the superseding apply cannot have
// succeeded already (that would have advanced lastSeq past
// this apply's sequence and been skipped instead). The
// re-fire's errors (if any) are returned to the applier:
// the apply itself succeeded, but the transport failed to
// rebind to it.
func (s *State) commitVLESSApply(
	nodeID types.NodeID,
	applied *vlessAppliedState,
	entry *xray.VLESSConfig,
	seq uint64,
) error {
	s.vlessMu.Lock()

	if applied.lastSeq > seq {
		// A concurrent apply succeeded after this apply stored
		// its override: that apply owns the node's state now.
		s.vlessMu.Unlock()

		return nil
	}

	var listeners []func(types.NodeID) error

	if entry != nil {
		if cur, ok := s.vlessOverrides[nodeID]; !ok || cur != entry {
			// Superseded while in flight: re-store the entry so
			// the read paths agree with the successful apply.
			s.vlessOverrides[nodeID] = entry
			listeners = slices.Clone(s.vlessListeners)
		}
	} else if _, ok := s.vlessOverrides[nodeID]; ok {
		// A successful clear superseded while in flight:
		// re-apply it so the read paths agree.
		delete(s.vlessOverrides, nodeID)
		listeners = slices.Clone(s.vlessListeners)
	}

	applied.last = entry
	applied.lastSeq = seq

	s.vlessMu.Unlock()

	if listeners == nil {
		return nil
	}

	return errors.Join(fireVLESSChangeListeners(listeners, nodeID)...)
}

// fireVLESSChangeListeners runs the registered per-node
// VLESS change listeners for the node and collects their
// errors.
func fireVLESSChangeListeners(listeners []func(types.NodeID) error, nodeID types.NodeID) []error {
	var errs []error

	for _, fn := range listeners {
		err := fn(nodeID)
		if err != nil {
			errs = append(errs, err)
		}
	}

	return errs
}

// NodeVLESS returns the operator-applied VLESS endpoint
// override for the node, or nil when the node uses its
// config-derived endpoint (the deterministic derivation over
// cfg.XRay). The returned config is a copy: mutating it does
// not affect the stored override.
func (s *State) NodeVLESS(nodeID types.NodeID) *xray.VLESSConfig {
	s.vlessMu.Lock()
	defer s.vlessMu.Unlock()

	cfg, ok := s.vlessOverrides[nodeID]
	if !ok {
		return nil
	}

	clone := *cfg

	return &clone
}

// OnNodeVLESSChange registers fn to run after every per-node
// VLESS override store or clear, with the affected node ID.
// The StealthScale transport registers the per-node listener
// (re)bind here so a Web UI-applied endpoint override takes
// effect without a restart. A listener error is returned to
// the caller of SetNodeVLESS (joined when several listeners
// are registered).
func (s *State) OnNodeVLESSChange(fn func(types.NodeID) error) {
	s.vlessMu.Lock()
	defer s.vlessMu.Unlock()

	s.vlessListeners = append(s.vlessListeners, fn)
}
