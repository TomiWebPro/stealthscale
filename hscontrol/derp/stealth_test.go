// Copyright (c) 2026 TomiWebPro <TomiWebPro@gmail.com>
// SPDX-License-Identifier: BSD-3-Clause

package derp

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
