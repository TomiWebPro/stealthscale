package webui

import (
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/tomiwebpro/stealthscale/hscontrol/types"
    "tailscale.com/tailcfg"
    "tailscale.com/types/views"
    "context"
)

type stubState struct {
    validKey string
}
func (s *stubState) ListNodes(ids ...types.NodeID) views.Slice[types.NodeView] { return views.Slice[types.NodeView]{} }
func (s *stubState) ListAllUsers() ([]types.User, error) { return nil, nil }
func (s *stubState) ListPreAuthKeys() ([]types.PreAuthKey, error) { return nil, nil }
func (s *stubState) GetPolicy() (*types.Policy, error) { return nil, nil }
func (s *stubState) DERPMap() tailcfg.DERPMapView { return tailcfg.DERPMapView{} }
func (s *stubState) PingDB(ctx context.Context) error { return nil }
func (s *stubState) ValidateAPIKey(k string) (bool, error) { return k==s.validKey, nil }
func (s *stubState) AuthenticateAccessToken(k string) (any, error) {
    if k==s.validKey { return struct{}{}, nil }
    return nil, assert.AnError
}

func testCfg() *types.Config {
    return &types.Config{XRay: types.XRayConfig{Enabled: true, Stealth: types.StealthConfig{Enforce: true, EnforceControl: true}}}
}

func TestWebUI_Auth401(t *testing.T) {
    cfg := testCfg()
    st := &stubState{validKey: "valid123"}
    h := Handler(cfg, st)
    // no header -> 401
    req := httptest.NewRequest(http.MethodGet, "/web/api/nodes", nil)
    w := httptest.NewRecorder()
    h.ServeHTTP(w, req)
    assert.Equal(t, http.StatusUnauthorized, w.Code)
    // garbage -> 401
    req = httptest.NewRequest(http.MethodGet, "/web/api/nodes", nil)
    req.Header.Set("Authorization", "Bearer garbage")
    w = httptest.NewRecorder()
    h.ServeHTTP(w, req)
    assert.Equal(t, http.StatusUnauthorized, w.Code)
    // valid -> 200 (nodes handler returns empty list)
    req = httptest.NewRequest(http.MethodGet, "/web/api/nodes", nil)
    req.Header.Set("Authorization", "Bearer valid123")
    w = httptest.NewRecorder()
    h.ServeHTTP(w, req)
    assert.Equal(t, http.StatusOK, w.Code)
    // X-API-Key valid
    req = httptest.NewRequest(http.MethodGet, "/web/api/nodes", nil)
    req.Header.Set("X-API-Key", "valid123")
    w = httptest.NewRecorder()
    h.ServeHTTP(w, req)
    assert.Equal(t, http.StatusOK, w.Code)
    // static frontend also gated
    req = httptest.NewRequest(http.MethodGet, "/web/", nil)
    w = httptest.NewRecorder()
    h.ServeHTTP(w, req)
    assert.Equal(t, http.StatusUnauthorized, w.Code)
    req = httptest.NewRequest(http.MethodGet, "/web/", nil)
    req.Header.Set("Authorization", "Bearer valid123")
    w = httptest.NewRecorder()
    h.ServeHTTP(w, req)
    // frontend may be 200 or 404 but not 401
    assert.NotEqual(t, http.StatusUnauthorized, w.Code)
}
