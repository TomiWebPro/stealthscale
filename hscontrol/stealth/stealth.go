// Package stealth gates DERP fallback on VLESS+Reality_XTLS health.
//
// StealthScale defaults to VLESS+Reality_XTLS (reality_xtls) via XTLS+uTLS.
// DERP relays are fingerprintable; stealth mode therefore gates DERP fallback:
// if stealth checks fail, DERP is suppressed (fail-closed) rather than leaking
// relay traffic. This is the "derp fallback should consider stealth and only
// implement if stealth is satisfied" requirement.
package stealth

import (
	"sync"

	"github.com/tomiwebpro/stealthscale/hscontrol/types"
	"tailscale.com/tailcfg"
)

// Checker holds stealth verification state. It gates DERP fallback on whether
// the VLESS+Reality stealth transport is actually serving. When the stealth
// transport is not ready, DERP is suppressed (fail-closed) so fingerprintable
// relay traffic never leaks.
type Checker struct {
	cfg     *types.XRayConfig
	mu      sync.RWMutex
	ready   bool
	healthy bool
}

// New creates a Checker from XRay config. If XRay is disabled or stealth
// not enforced, the checker always reports healthy (no gating).
func New(cfg *types.XRayConfig) *Checker {
	return &Checker{
		cfg:     cfg,
		healthy: true,
	}
}

// SetReady records whether the stealth transport is currently serving.
// app.go calls this after the xray listeners start (or stop). When stealth is
// enforced and the transport is not ready, DERP fallback is suppressed.
func (c *Checker) SetReady(ready bool) {
	c.mu.Lock()
	c.ready = ready
	c.mu.Unlock()
}

// IsSatisfied reports whether stealth is satisfied and DERP fallback is allowed.
// When Stealth.Enforce is false, always true. When true, DERP is only offered
// while the reality_xtls transport is actually serving (fail-closed).
func (c *Checker) IsSatisfied() bool {
	if c.cfg == nil || !c.cfg.Enabled {
		return true
	}
	if !c.cfg.Stealth.Enforce {
		return true
	}
	sec := c.cfg.Security
	if sec == "reality" {
		sec = "reality_xtls"
	}
	if sec != "reality_xtls" {
		// Non-stealth transport -> stealth gating irrelevant
		return true
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ready
}

// MarkHealthy records a successful stealth probe (alias for SetReady(true)).
func (c *Checker) MarkHealthy() {
	c.SetReady(true)
}

// MarkUnhealthy records a failed stealth probe (alias for SetReady(false)).
func (c *Checker) MarkUnhealthy() {
	c.SetReady(false)
}

// ShouldIncludeDERP reports whether DERP regions should be included in the
// netmap for a node. When stealth is enforced and unsatisfied, DERP is
// suppressed (fail-closed).
func (c *Checker) ShouldIncludeDERP() bool {
	return c.IsSatisfied()
}

// FilterDERPMap returns the DERPMap filtered for stealth.
// If stealth is unsatisfied and enforce is true, returns a DERPMap with no regions
// (fail-closed). Otherwise returns original.
func (c *Checker) FilterDERPMap(dm *tailcfg.DERPMap) *tailcfg.DERPMap {
	if c.ShouldIncludeDERP() {
		return dm
	}
	// Fail-closed: suppress DERP when stealth not satisfied
	if dm == nil {
		return &tailcfg.DERPMap{Regions: map[int]*tailcfg.DERPRegion{}}
	}
	return &tailcfg.DERPMap{
		OmitDefaultRegions: dm.OmitDefaultRegions,
		Regions:            map[int]*tailcfg.DERPRegion{},
	}
}
