// Copyright (c) 2026 TomiWebPro <TomiWebPro@gmail.com>
// SPDX-License-Identifier: BSD-3-Clause

package derp

import (
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
)

// IsStealthSatisfied reports whether the VLESS+Reality stealth transport
// is satisfied for DERP fallback decisions. When stealth enforcement is
// enabled (default with reality_xtls), DERP relays are only offered when
// the stealth transport is active. If stealth is not satisfied, DERP
// fallback is suppressed (fail-closed) to avoid leaking fingerprintable
// relay traffic.
//
// Rules:
//   - If XRay is disabled or stealth.Enforce is false → satisfied (no gating)
//   - If security is reality_xtls (or alias "reality") → satisfied when utls
//     fingerprint is configured (defaults to "chrome") — this covers the
//     default stealth mode where Reality dest may be auto-derived from server_url
//   - Otherwise (none/tls/xtls with enforce true) → not satisfied → fail closed
func IsStealthSatisfied(cfg *types.XRayConfig) bool {
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
		// Reality transport is active — stealth satisfied. utls fingerprint
		// defaults to chrome when empty, so empty is still considered satisfied
		// for the default config.
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
