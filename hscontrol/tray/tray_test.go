package tray

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tomiwebpro/stealthscale/hscontrol/assets"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
)

func TestBuildWebURL(t *testing.T) {
	tests := []struct {
		name string
		cfg  types.Config
		want string
	}{
		{
			name: "empty addr defaults to 127.0.0.1:8080 http",
			cfg:  types.Config{Addr: ""},
			want: "http://127.0.0.1:8080/web",
		},
		{
			name: "0.0.0.0 normalized to 127.0.0.1",
			cfg:  types.Config{Addr: "0.0.0.0:8080"},
			want: "http://127.0.0.1:8080/web",
		},
		{
			name: ":: normalized",
			cfg:  types.Config{Addr: "[::]:8080"},
			want: "http://127.0.0.1:8080/web",
		},
		{
			name: "explicit host kept",
			cfg:  types.Config{Addr: "192.168.1.10:9090"},
			want: "http://192.168.1.10:9090/web",
		},
		{
			name: "https when CertPath set",
			cfg:  types.Config{Addr: "0.0.0.0:8443", TLS: types.TLSConfig{CertPath: "/etc/cert.crt"}},
			want: "https://127.0.0.1:8443/web",
		},
		{
			name: "https when LetsEncrypt hostname set",
			cfg:  types.Config{Addr: "127.0.0.1:443", TLS: types.TLSConfig{LetsEncrypt: types.LetsEncryptConfig{Hostname: "ctl.example.com"}}},
			want: "https://127.0.0.1:443/web",
		},
		{
			name: "invalid addr falls back to default",
			cfg:  types.Config{Addr: "not-an-addr"},
			want: "http://127.0.0.1:8080/web",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildWebURL(&tt.cfg)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsSupported(t *testing.T) {
	got := IsSupported()
	if runtime.GOOS == "windows" {
		assert.True(t, got, "IsSupported must be true on windows")
	} else {
		assert.False(t, got, "IsSupported must be false on %s", runtime.GOOS)
	}
}

func TestTrayIconEmbedded(t *testing.T) {
	// TrayIcon was generated from favicon.png via PIL to 256/48/32/16 ICO
	require.NotEmpty(t, assets.TrayIcon, "TrayIcon should be embedded (hscontrol/assets/tray.ico)")
	// ICO header: reserved 0, type 1 (icon), count >=1
	require.GreaterOrEqual(t, len(assets.TrayIcon), 6, "ICO too small")
	assert.Equal(t, byte(0), assets.TrayIcon[0], "ICO reserved must be 0")
	assert.Equal(t, byte(0), assets.TrayIcon[1], "ICO reserved must be 0")
	assert.Equal(t, byte(1), assets.TrayIcon[2], "ICO type must be 1 (icon)")
	assert.Equal(t, byte(0), assets.TrayIcon[3], "ICO type high byte must be 0")
	// Favicon PNG should also be present (used as fallback on non-windows)
	require.NotEmpty(t, assets.Favicon, "Favicon should be embedded")
}

func TestNeedsTray(t *testing.T) {
	// NeedsTray is defined on windows only; on other OS it does not exist.
	// We test via IsSupported gating.
	if IsSupported() {
		// windows
		cfg := &types.Config{Addr: "0.0.0.0:8080"}
		// via interface existence check - if compiled on windows, NeedsTray exists
		// This test validates the function does not panic.
		_ = NeedsTray(cfg)
	}
}
