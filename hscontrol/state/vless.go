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
	if cfg == nil {
		delete(s.vlessOverrides, nodeID)
	} else {
		stored := *cfg
		s.vlessOverrides[nodeID] = &stored
	}

	listeners := slices.Clone(s.vlessListeners)
	s.vlessMu.Unlock()

	var errs []error

	for _, fn := range listeners {
		err := fn(nodeID)
		if err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
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
