// Copyright (c) 2026 TomiWebPro <TomiWebPro@gmail.com>
// SPDX-License-Identifier: BSD-3-Clause

package servertest

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tomiwebpro/stealthscale/hscontrol/derp"
	"github.com/tomiwebpro/stealthscale/hscontrol/stealth"
)

// TestDERPFailClosedViaStealth verifies that DERP is fail-closed when Reality
// is not satisfied, and is offered when it is. This is the servertest counterpart
// to the integration TestDERPFailClosed.
func TestDERPFailClosedViaStealth(t *testing.T) {
	// Server with stealth enforce but XRay not started => not ready => DERP suppressed
	ts := NewServer(t, WithXRay("127.0.0.1", 42000, 42010))
	// Manually enable stealth enforce for this test (WithXRay defaults to plain)
	ts.Cfg.XRay.Stealth.Enforce = true
	ts.Cfg.XRay.Security = "reality_xtls"
	// Also set reality dest to avoid default but not needed for this check
	ts.Cfg.XRay.Reality.Dest = "www.cloudflare.com:443"

	// Register a checker and mark not ready
	checker := stealth.New(&ts.Cfg.XRay)
	checker.SetReady(false)
	derp.SetStealthChecker(checker)
	defer derp.SetStealthChecker(nil)

	require.False(t, derp.IsStealthSatisfied(&ts.Cfg.XRay), "stealth not ready => IsStealthSatisfied false")
	require.False(t, derp.ShouldIncludeDERP(ts.Cfg), "DERP should be suppressed when stealth not satisfied")

	// FilterDERPMap should return empty regions
	origDERP := ts.State().DERPMap().AsStruct()
	require.NotEmpty(t, origDERP.Regions, "test DERP map should have regions")
	filtered := checker.FilterDERPMap(origDERP)
	require.Empty(t, filtered.Regions, "filtered DERPMap should be empty when stealth not satisfied")

	// Now mark stealth ready (as app.go does after StartXRayServer)
	checker.SetReady(true)
	require.True(t, derp.IsStealthSatisfied(&ts.Cfg.XRay), "stealth ready => satisfied")
	require.True(t, derp.ShouldIncludeDERP(ts.Cfg), "DERP should be included when stealth satisfied")
	filtered2 := checker.FilterDERPMap(origDERP)
	require.NotEmpty(t, filtered2.Regions, "DERPMap should be populated when stealth satisfied")
	require.Equal(t, len(origDERP.Regions), len(filtered2.Regions))
}

// TestDERPHTTPGate verifies the predicate that gateDERPOnStealth uses.
// When stealth is not satisfied, ShouldIncludeDERP is false and the handler
// would return 421; when satisfied it returns true and the request is proxied.
func TestDERPHTTPGate(t *testing.T) {
	ts := NewServer(t, WithXRay("127.0.0.1", 42100, 42110))
	ts.Cfg.XRay.Stealth.Enforce = true
	ts.Cfg.XRay.Security = "reality_xtls"
	ts.Cfg.XRay.Reality.Dest = "www.cloudflare.com:443"

	checker := stealth.New(&ts.Cfg.XRay)
	derp.SetStealthChecker(checker)
	defer derp.SetStealthChecker(nil)

	// Not ready => gated
	checker.SetReady(false)
	require.False(t, derp.ShouldIncludeDERP(ts.Cfg), "gate should be closed when stealth not ready")
	// Simulate handler gating: would return 421
	require.False(t, derp.IsStealthSatisfied(&ts.Cfg.XRay))

	// Ready => open
	checker.SetReady(true)
	require.True(t, derp.ShouldIncludeDERP(ts.Cfg), "gate should be open when stealth ready")
	require.True(t, derp.IsStealthSatisfied(&ts.Cfg.XRay))
}

// TestDERPNoLeakExternalURLs ensures that when stealth is enforced, external
// DERP urls (default empty) are not leaked. The default config has urls: [] and
// derp.server.verify_clients:true, so no topology is exposed to third parties.
func TestDERPNoLeakExternalURLs(t *testing.T) {
	ts := NewServer(t, WithXRay("127.0.0.1", 42200, 42210))
	// Default has no external DERP urls
	require.Empty(t, ts.Cfg.DERP.URLs, "default DERP urls should be empty (no leak)")
	require.Empty(t, ts.Cfg.DERP.Paths)
	// When stealth not satisfied, DERPMap should be empty even though test map has regions
	checker := stealth.New(&ts.Cfg.XRay)
	ts.Cfg.XRay.Stealth.Enforce = true
	ts.Cfg.XRay.Security = "reality_xtls"
	checker.SetReady(false)
	derp.SetStealthChecker(checker)
	defer derp.SetStealthChecker(nil)

	orig := ts.State().DERPMap().AsStruct()
	require.NotEmpty(t, orig.Regions)
	filtered := checker.FilterDERPMap(orig)
	require.Empty(t, filtered.Regions)
}
