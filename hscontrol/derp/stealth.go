// Copyright (c) 2026 TomiWebPro <TomiWebPro@gmail.com>
// SPDX-License-Identifier: BSD-3-Clause

package derp

import (
	"github.com/tomiwebpro/stealthscale/hscontrol/stealth"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
)

// globalChecker, when set by the server, provides a runtime stealth readiness
// signal (driven by whether the VLESS+Reality transport is actually serving).
// When set, IsStealthSatisfied consults it; otherwise it falls back to the
// static config-string check below.
var globalChecker *stealth.Checker

// SetStealthChecker registers the runtime stealth checker used to gate DERP.
// The server calls this once the xray transport has started (or stopped).
func SetStealthChecker(c *stealth.Checker) {
	globalChecker = c
}

// MarkStealthReady flips the runtime readiness flag used to gate DERP, so that
// fail-closed DERP suppression is lifted only once the stealth transport is
// actually serving.
func MarkStealthReady() {
	if globalChecker != nil {
		globalChecker.SetReady(true)
	}
}

// IsStealthSatisfied reports whether the VLESS+Reality stealth transport
// is satisfied for DERP fallback decisions. When stealth enforcement is
// enabled (default with reality_xtls), DERP relays are only offered when
// the stealth transport is active. If stealth is not satisfied, DERP
// fallback is suppressed (fail-closed) to avoid leaking fingerprintable
// relay traffic.
//
// Rules:
//   - If a runtime checker is registered, its readiness is authoritative.
//   - If XRay is disabled or stealth.Enforce is false → satisfied (no gating)
//   - If security is reality_xtls (or alias "reality") and no runtime checker
//     is registered → satisfied (config-time optimistic default)
//   - Otherwise (none/tls/xtls with enforce true) → not satisfied → fail closed
func IsStealthSatisfied(cfg *types.XRayConfig) bool {
	if globalChecker != nil {
		return globalChecker.IsSatisfied()
	}
	if cfg == nil {
		return true
	}
	if !cfg.Enabled {
		return true
	}
	if !cfg.Stealth.Enforce {
		return true
	}
	sec := cfg.Security
	if sec == "reality" {
		sec = "reality_xtls"
	}
	if sec == "" {
		sec = "reality_xtls"
	}
	switch sec {
	case "reality_xtls":
		// Reality transport is configured — without a runtime checker we assume
		// the server will set readiness once listeners are up.
		return true
	default:
		// Enforced but not on stealth transport → fail closed, no DERP fallback
		return false
	}
}

// ShouldIncludeDERP reports whether the DERP map should be included in a
// MapResponse for the given global config. It is a convenience wrapper
// around IsStealthSatisfied for the mapper.
func ShouldIncludeDERP(cfg *types.Config) bool {
	if cfg == nil {
		return true
	}
	return IsStealthSatisfied(&cfg.XRay)
}
