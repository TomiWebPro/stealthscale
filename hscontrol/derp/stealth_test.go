// Copyright (c) 2026 TomiWebPro <TomiWebPro@gmail.com>
// SPDX-License-Identifier: BSD-3-Clause

package derp

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tomiwebpro/stealthscale/hscontrol/stealth"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
)

func xray(enabled bool, security string, enforce bool) *types.XRayConfig {
	return &types.XRayConfig{
		Enabled:  enabled,
		Security: security,
		Stealth:  types.StealthConfig{Enforce: enforce},
	}
}

func TestIsStealthSatisfied(t *testing.T) {
	tests := []struct {
		name string
		cfg  *types.XRayConfig
		want bool
	}{
		{"nil config", nil, true},
		{"xray disabled", xray(false, "reality_xtls", true), true},
		{"stealth not enforced", xray(true, "none", false), true},
		{"reality_xtls enforced", xray(true, "reality_xtls", true), true},
		{"reality alias enforced", xray(true, "reality", true), true},
		{"empty security defaults to reality_xtls", xray(true, "", true), true},
		{"none enforced fails closed", xray(true, "none", true), false},
		{"tls enforced fails closed", xray(true, "tls", true), false},
		{"xtls enforced fails closed", xray(true, "xtls", true), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsStealthSatisfied(tt.cfg))
		})
	}
}

func TestShouldIncludeDERP(t *testing.T) {
	assert.True(t, ShouldIncludeDERP(nil))

	cfg := &types.Config{XRay: types.XRayConfig{Enabled: true, Security: "none", Stealth: types.StealthConfig{Enforce: true}}}
	assert.False(t, ShouldIncludeDERP(cfg), "enforced non-stealth transport must suppress DERP")

	cfg.XRay.Security = "reality_xtls"
	assert.True(t, ShouldIncludeDERP(cfg), "stealth transport keeps DERP available")
}

// --- Runtime stealth checker (globalChecker) wiring ----------------------
//
// app.go registers a runtime stealth.Checker via SetStealthChecker once
// the xray transport starts, then calls MarkStealthReady once the
// listeners are serving. While registered, the checker is authoritative
// for IsStealthSatisfied, overriding the config-only optimistic default,
// so DERP stays fail-closed until the stealth transport is actually
// serving. The tests above only exercise the config-only fallback path
// (globalChecker == nil); the runtime path production uses had no
// coverage — a regression that dropped the globalChecker early-return
// would silently fail OPEN (advertise DERP before the stealth transport
// serves) while every existing test stayed green.

// fullConfig wraps an XRay config in a types.Config for
// ShouldIncludeDERP, which reads only the XRay section.
func fullConfig(cfg *types.XRayConfig) *types.Config {
	var c types.Config

	c.XRay = *cfg

	return &c
}

func TestRuntimeStealthCheckerAuthoritative(t *testing.T) {
	cfg := xray(true, "reality_xtls", true)

	// Config-only fallback is optimistic for reality_xtls...
	assert.True(t, IsStealthSatisfied(cfg))

	c := stealth.New(cfg)
	SetStealthChecker(c)
	t.Cleanup(func() { SetStealthChecker(nil) })

	// ...but a registered, not-yet-ready checker fails closed: DERP must
	// be suppressed until the stealth transport reports serving.
	assert.False(t, IsStealthSatisfied(cfg),
		"registered unready checker must fail closed even for reality_xtls")
	assert.False(t, ShouldIncludeDERP(fullConfig(cfg)),
		"DERP map must be suppressed while the stealth transport is not serving")

	// Once the transport reports ready, DERP fallback is allowed.
	MarkStealthReady()

	assert.True(t, IsStealthSatisfied(cfg))
	assert.True(t, ShouldIncludeDERP(fullConfig(cfg)))

	// A failed probe flips back to fail-closed.
	c.MarkUnhealthy()

	assert.False(t, IsStealthSatisfied(cfg))
	assert.False(t, ShouldIncludeDERP(fullConfig(cfg)))
}

func TestRuntimeStealthCheckerAppliesToAnySecurity(t *testing.T) {
	// Per the stealth.Checker contract, readiness applies to any security
	// setting when stealth is enforced: unready fails closed, ready allows
	// DERP. Pin those documented semantics so the two code paths
	// (config-only fallback vs runtime checker) stay intentional.
	for _, sec := range []string{"reality_xtls", "none", "tls", "xtls"} {
		cfg := xray(true, sec, true)
		c := stealth.New(cfg)
		SetStealthChecker(c)
		t.Cleanup(func() { SetStealthChecker(nil) })

		assert.False(t, IsStealthSatisfied(cfg), "sec=%s: unready must fail closed", sec)

		c.SetReady(true)
		assert.True(t, IsStealthSatisfied(cfg), "sec=%s: ready must allow DERP", sec)
	}
}

func TestMarkStealthReadyWithoutCheckerIsNoOp(t *testing.T) {
	// Must not panic when no runtime checker is registered (e.g. xray
	// disabled or stealth not enforced, per app.go's guard), and the
	// config-only path stays authoritative afterwards.
	SetStealthChecker(nil)
	assert.NotPanics(t, MarkStealthReady)
	assert.False(t, IsStealthSatisfied(xray(true, "none", true)),
		"config-only path still fails closed for enforced non-stealth transport")
	assert.True(t, IsStealthSatisfied(xray(true, "reality_xtls", true)),
		"config-only path still optimistic for reality_xtls")
}

func TestRuntimeStealthCheckerConcurrent(t *testing.T) {
	// MarkStealthReady/IsStealthSatisfied run on request goroutines
	// (gateDERPOnStealth) and the ticker; pin race-free readiness
	// updates while registered.
	cfg := xray(true, "reality_xtls", true)
	c := stealth.New(cfg)
	SetStealthChecker(c)
	t.Cleanup(func() { SetStealthChecker(nil) })

	var wg sync.WaitGroup

	for range 50 {
		wg.Add(2)

		go func() {
			defer wg.Done()

			MarkStealthReady()

			_ = IsStealthSatisfied(cfg)
		}()

		go func() {
			defer wg.Done()

			c.MarkUnhealthy()

			_ = ShouldIncludeDERP(fullConfig(cfg))
		}()
	}

	wg.Wait()

	// Final state is racy but must not panic under -race.
	_ = IsStealthSatisfied(cfg)
}
