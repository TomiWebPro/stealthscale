// Copyright (c) 2026 TomiWebPro <TomiWebPro@gmail.com>
// SPDX-License-Identifier: BSD-3-Clause

package servertest

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tomiwebpro/stealthscale/hscontrol/derp"
	"github.com/tomiwebpro/stealthscale/hscontrol/stealth"
	"github.com/tomiwebpro/stealthscale/hscontrol/webui"
)

// TestDERPRuntimeApplyReachesConnectedClient pins the
// operator flow promised by docs/usage/webui.md: a DERP
// map applied via POST /web/api/derp is broadcast to
// connected clients immediately. The Web UI surface
// delegates to state.SetDERPMapRuntime; the state's
// OnDERPMapChange hook (registered by StealthScale.Serve
// and mirrored by servertest.NewServer) pushes a DERPMap
// change through the mapper batcher, so already-connected
// clients receive the new map on their live long-poll —
// no reconnect, no poll restart, no waiting for the next
// periodic map response.
//
// Failure mode guarded: the store updates but the
// broadcast is missing (e.g. the harness or a future
// entry point forgets the hook) — connected clients keep
// serving the stale netmap while the state and the Web
// UI report the new map.
func TestDERPRuntimeApplyReachesConnectedClient(t *testing.T) {
	t.Parallel()

	h := NewHarness(t, 2)

	st := h.Server.State()

	// Baseline: the harness seed map (region 900) is
	// live in both connected clients' netmaps.
	for _, tc := range h.Clients() {
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			nm := tc.Netmap()
			require.NotNil(c, nm)
			require.NotNil(c, nm.DERPMap)
			assert.Contains(c, nm.DERPMap.Regions, 900)
		}, 5*time.Second, 50*time.Millisecond, "client should see the seed DERP map")
	}

	// The operator applies a new DERP map through the
	// Web UI surface — the real handler against the real
	// state, the same delegation path as production.
	wh := webui.Handler(h.Server.Cfg, st)

	ts := httptest.NewServer(wh)
	defer ts.Close()

	applied := map[string]any{
		"derpMap": map[string]any{
			"regions": map[string]any{
				"901": map[string]any{
					"regionID":   901,
					"regionCode": "rt",
					"regionName": "Runtime Applied",
					"nodes": []any{
						map[string]any{
							"name":     "rt0",
							"regionID": 901,
							"hostName": "127.0.0.1",
							"ipv4":     "127.0.0.1",
							"derpPort": -1,
						},
					},
				},
			},
		},
	}

	body, err := json.Marshal(applied)
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		ts.URL+"/web/api/derp",
		bytes.NewReader(body),
	)
	require.NoError(t, err)

	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode, "runtime DERP apply must succeed")
	require.True(t, st.DERPMapIsRuntime(), "the applied map must be marked operator-applied")

	// The broadcast must reach every connected client:
	// both netmaps converge to the applied map (region
	// 901 present, seed region 900 gone) with no
	// reconnect or poll restart.
	for _, tc := range h.Clients() {
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			nm := tc.Netmap()
			require.NotNil(c, nm)
			require.NotNil(c, nm.DERPMap)
			assert.Contains(c, nm.DERPMap.Regions, 901)
			assert.NotContains(c, nm.DERPMap.Regions, 900)
		}, 5*time.Second, 50*time.Millisecond, "connected client must receive the applied DERP map")
	}
}

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
