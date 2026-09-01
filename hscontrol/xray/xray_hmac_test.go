package xray

import (
    "os"
    "path/filepath"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    "github.com/gofrs/uuid/v5"
    "github.com/tomiwebpro/stealthscale/hscontrol/types"
)

func TestNodeUUID_HMAC_Determinism(t *testing.T) {
    id := types.NodeID(42)
    s1 := "test-secret-for-hmac-32bytes-hex-1234567890abcdef"
    s2 := "different-secret-1234567890abcdef1234567890abcdef"
    a1 := NodeUUID(id, s1)
    a2 := NodeUUID(id, s1)
    b1 := NodeUUID(id, s2)
    empty := NodeUUID(id, "")
    assert.Equal(t, a1, a2, "HMAC determinism")
    assert.NotEqual(t, a1, b1, "different secrets -> different UUIDs")
    assert.NotEqual(t, a1, empty, "empty vs HMAC differs")
    u, err := uuid.FromString(a1)
    require.NoError(t, err)
    assert.Equal(t, uuid.V5, u.Version())
}

func TestNodePort_HMAC_Determinism(t *testing.T) {
    minP, maxP := 10001, 10100
    id := types.NodeID(7)
    s := "test-secret-port-1234567890abcdef1234567890abcdef"
    p1 := NodePort(id, s, minP, maxP)
    p2 := NodePort(id, s, minP, maxP)
    assert.Equal(t, p1, p2)
    assert.GreaterOrEqual(t, p1, minP)
    assert.LessOrEqual(t, p1, maxP)
    // degenerate still returns min
    assert.Equal(t, 443, NodePort(types.NodeID(1), s, 443, 443))
    // distribution with secret
    ports := make(map[int]bool)
    for i:=1;i<=50;i++{
        ports[NodePort(types.NodeID(i), s, minP, maxP)]=true
    }
    assert.Greater(t, len(ports), 1)
}

func TestInitIdentity_Stability(t *testing.T) {
    dir := t.TempDir()
    cfg := &types.XRayConfig{}
    require.NoError(t, cfg.InitIdentity(dir))
    secret1 := cfg.Secret
    require.NotEmpty(t, secret1)
    // second load from same dir stable
    cfg2 := &types.XRayConfig{}
    require.NoError(t, cfg2.InitIdentity(dir))
    assert.Equal(t, secret1, cfg2.Secret)
    assert.Equal(t, NodeUUID(types.NodeID(1), secret1), NodeUUID(types.NodeID(1), cfg2.Secret))
    // different dir different secret
    dir2 := t.TempDir()
    cfg3 := &types.XRayConfig{}
    require.NoError(t, cfg3.InitIdentity(dir2))
    assert.NotEqual(t, secret1, cfg3.Secret)
    // fallback enumerable warning: empty secret is predictable but documented
    predictable := NodeUUID(types.NodeID(42), "")
    assert.NotEmpty(t, predictable)
    _ = os.MkdirAll  // keep import
    _ = filepath.Join // keep import
}
