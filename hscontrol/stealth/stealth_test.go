package stealth

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
	"tailscale.com/tailcfg"
)

func TestCheckerDisabled(t *testing.T) {
	// No config => always satisfied
	c := New(nil)
	assert.True(t, c.IsSatisfied())
	assert.True(t, c.ShouldIncludeDERP())

	// XRay disabled => always satisfied regardless of Enforce
	cfg := &types.XRayConfig{Enabled: false, Stealth: types.StealthConfig{Enforce: true}, Security: "reality_xtls"}
	c = New(cfg)
	assert.True(t, c.IsSatisfied())
}

func TestCheckerEnforceFalse(t *testing.T) {
	cfg := &types.XRayConfig{Enabled: true, Stealth: types.StealthConfig{Enforce: false}, Security: "reality_xtls"}
	c := New(cfg)
	c.SetReady(false)
	assert.True(t, c.IsSatisfied(), "enforce:false must always be satisfied even when not ready")
}

func TestCheckerRealityRequiresReady(t *testing.T) {
	cfg := &types.XRayConfig{Enabled: true, Stealth: types.StealthConfig{Enforce: true}, Security: "reality_xtls"}
	c := New(cfg)
	c.SetReady(false)
	assert.False(t, c.IsSatisfied(), "reality_xtls with enforce:true and not ready => not satisfied")
	c.SetReady(true)
	assert.True(t, c.IsSatisfied())
	c.MarkUnhealthy()
	assert.False(t, c.IsSatisfied())
	c.MarkHealthy()
	assert.True(t, c.IsSatisfied())
}

func TestCheckerNonRealityFailClosedWhenEnforced(t *testing.T) {
	for _, sec := range []string{"none", "tls", "xtls"} {
		cfg := &types.XRayConfig{Enabled: true, Stealth: types.StealthConfig{Enforce: true}, Security: sec}
		c := New(cfg)
		c.SetReady(false)
		assert.False(t, c.IsSatisfied(), "sec=%s enforce:true not ready => fail-closed", sec)
		c.SetReady(true)
		assert.True(t, c.IsSatisfied(), "sec=%s enforce:true ready => satisfied", sec)
	}
	// alias "reality" normalises to reality_xtls
	cfg := &types.XRayConfig{Enabled: true, Stealth: types.StealthConfig{Enforce: true}, Security: "reality"}
	c := New(cfg)
	c.SetReady(false)
	assert.False(t, c.IsSatisfied())
}

func TestCheckerConcurrent(t *testing.T) {
	cfg := &types.XRayConfig{Enabled: true, Stealth: types.StealthConfig{Enforce: true}, Security: "reality_xtls"}
	c := New(cfg)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); c.SetReady(true); _ = c.IsSatisfied() }()
		go func() { defer wg.Done(); c.SetReady(false); _ = c.IsSatisfied() }()
	}
	wg.Wait()
	// Final state is racy but must not panic under -race
	_ = c.IsSatisfied()
}

func TestFilterDERPMap(t *testing.T) {
	cfg := &types.XRayConfig{Enabled: true, Stealth: types.StealthConfig{Enforce: true}, Security: "reality_xtls"}
	c := New(cfg)

	dm := &tailcfg.DERPMap{
		Regions: map[int]*tailcfg.DERPRegion{
			1: {RegionID: 1},
		},
	}

	// Not ready => empty
	c.SetReady(false)
	filtered := c.FilterDERPMap(dm)
	require.NotNil(t, filtered)
	assert.Empty(t, filtered.Regions)
	assert.Equal(t, dm.OmitDefaultRegions, filtered.OmitDefaultRegions)
	// original not mutated
	assert.Len(t, dm.Regions, 1)

	// Ready => original returned
	c.SetReady(true)
	filtered = c.FilterDERPMap(dm)
	assert.Same(t, dm, filtered)

	// nil input when not ready => empty map not nil
	c.SetReady(false)
	filtered = c.FilterDERPMap(nil)
	require.NotNil(t, filtered)
	assert.Empty(t, filtered.Regions)
}

func TestFilterDERPMapNoEnforce(t *testing.T) {
	cfg := &types.XRayConfig{Enabled: true, Stealth: types.StealthConfig{Enforce: false}, Security: "none"}
	c := New(cfg)
	c.SetReady(false)
	dm := &tailcfg.DERPMap{Regions: map[int]*tailcfg.DERPRegion{1: {RegionID: 1}}}
	assert.Same(t, dm, c.FilterDERPMap(dm))
}
