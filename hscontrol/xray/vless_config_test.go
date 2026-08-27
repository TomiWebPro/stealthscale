// Copyright (c) 2026 TomiWebPro <TomiWebPro@gmail.com>
// SPDX-License-Identifier: BSD-3-Clause

package xray

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVLESSConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *VLESSConfig
		wantErr bool
	}{
		{"valid", &VLESSConfig{ID: "u", Address: "1.2.3.4", Port: 10001, Security: "reality_xtls"}, false},
		{"valid none", &VLESSConfig{ID: "u", Address: "1.2.3.4", Port: 10001, Security: "none"}, false},
		{"valid reality alias", &VLESSConfig{ID: "u", Address: "1.2.3.4", Port: 10001, Security: "reality"}, false},
		{"missing id", &VLESSConfig{Address: "1.2.3.4", Port: 10001, Security: "none"}, true},
		{"missing address", &VLESSConfig{ID: "u", Port: 10001, Security: "none"}, true},
		{"port zero", &VLESSConfig{ID: "u", Address: "1.2.3.4", Port: 0, Security: "none"}, true},
		{"port too high", &VLESSConfig{ID: "u", Address: "1.2.3.4", Port: 70000, Security: "none"}, true},
		{"bad security", &VLESSConfig{ID: "u", Address: "1.2.3.4", Port: 10001, Security: "bogus"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestVLESSConfigURI(t *testing.T) {
	// reality_xtls default includes uTLS + flow hints.
	c := &VLESSConfig{ID: "abc", Address: "10.0.0.1", Port: 10001, Security: "reality_xtls"}
	uri := c.URI()
	assert.Contains(t, uri, "vless://abc@10.0.0.1:10001")
	assert.Contains(t, uri, "security=reality_xtls")
	assert.Contains(t, uri, "fp=chrome")
	assert.Contains(t, uri, "flow=xtls-rprx-vision")

	// empty security defaults to reality_xtls.
	assert.Contains(t, (&VLESSConfig{ID: "x", Address: "h", Port: 1}).URI(), "security=reality_xtls")

	// "reality" alias normalised to reality_xtls.
	assert.Contains(t, (&VLESSConfig{ID: "x", Address: "h", Port: 1, Security: "reality"}).URI(), "security=reality_xtls")

	// none keeps a plain URI without reality hints.
	none := (&VLESSConfig{ID: "x", Address: "h", Port: 1, Security: "none"}).URI()
	assert.Contains(t, none, "security=none")
	assert.NotContains(t, none, "fp=chrome")
}

func TestVLESSConfigJSON(t *testing.T) {
	c := NewVLESSConfig("id-1", "127.0.0.1", 20001)
	assert.Equal(t, "none", c.Security)
	assert.Equal(t, 30*time.Second, c.Timeout)

	data, err := c.ToJSON()
	require.NoError(t, err)

	var got VLESSConfig
	require.NoError(t, got.FromJSON(data))
	assert.Equal(t, c.ID, got.ID)
	assert.Equal(t, c.Address, got.Address)
	assert.Equal(t, c.Port, got.Port)

	require.NoError(t, ValidateVLESSConfig(c))
	assert.Error(t, ValidateVLESSConfig(&VLESSConfig{}))
}
