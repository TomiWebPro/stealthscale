package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
	"net/netip"
	"tailscale.com/tailcfg"
	"tailscale.com/types/views"
)

type mockState struct {
	nodes   []types.NodeView
	users   []types.User
	keys    []types.PreAuthKey
	policy  *types.Policy
	derpMap tailcfg.DERPMapView
	pingErr error
}

func (m *mockState) ListNodes(nodeIDs ...types.NodeID) views.Slice[types.NodeView] {
	if len(nodeIDs) == 0 {
		return views.SliceOf(m.nodes)
	}
	// filter
	set := map[types.NodeID]struct{}{}
	for _, id := range nodeIDs {
		set[id] = struct{}{}
	}
	var out []types.NodeView
	for _, n := range m.nodes {
		if _, ok := set[n.ID()]; ok {
			out = append(out, n)
		}
	}
	return views.SliceOf(out)
}
func (m *mockState) ListAllUsers() ([]types.User, error)          { return m.users, nil }
func (m *mockState) ListPreAuthKeys() ([]types.PreAuthKey, error) { return m.keys, nil }
func (m *mockState) GetPolicy() (*types.Policy, error) {
	if m.policy == nil {
		return nil, nil
	}
	return m.policy, nil
}
func (m *mockState) DERPMap() tailcfg.DERPMapView     { return m.derpMap }
func (m *mockState) PingDB(ctx context.Context) error { return m.pingErr }

func testConfig() *types.Config {
	return &types.Config{
		ServerURL: "https://ctl.example.com",
		XRay: types.XRayConfig{
			Enabled:        true,
			ListenAddr:     "0.0.0.0",
			BaseListenPort: 10001,
			MaxListenPort:  10100,
			Security:       "reality_xtls",
			Reality: types.RealityConfig{
				Dest: "www.microsoft.com:443",
			},
			Stealth: types.StealthConfig{
				Enforce: true,
			},
		},
	}
}

func TestWebUI_Embedded(t *testing.T) {
	cfg := testConfig()
	st := &mockState{}
	h := Handler(cfg, st)
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/web/")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/html")

	resp2, err := http.Get(srv.URL + "/admin/")
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode)
}

func TestWebUI_APIs(t *testing.T) {
	cfg := testConfig()
	st := &mockState{
		users:  []types.User{{Name: "alice", Email: "alice@example.com"}},
		keys:   []types.PreAuthKey{{ID: 1, Key: "test-key"}},
		policy: &types.Policy{Data: `{"acls":[]}`},
	}
	// Use DERPMap default
	h := Handler(cfg, st)
	srv := httptest.NewServer(h)
	defer srv.Close()

	endpoints := []string{
		"/web/api/nodes",
		"/web/api/users",
		"/web/api/preauthkeys",
		"/web/api/policy",
		"/web/api/derp",
		"/web/api/health",
		"/admin/api/nodes",
		"/admin/api/health",
	}
	for _, ep := range endpoints {
		resp, err := http.Get(srv.URL + ep)
		require.NoError(t, err, ep)
		assert.Equal(t, http.StatusOK, resp.StatusCode, ep)
		var body map[string]any
		err = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		require.NoError(t, err, ep)
	}

	// VLESS requires node id
	resp, err := http.Get(srv.URL + "/web/api/vless/1")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var v map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&v))
	resp.Body.Close()
	assert.Contains(t, v["uri"], "vless://")

	// missing id -> 400
	resp, err = http.Get(srv.URL + "/web/api/vless/")
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// invalid id -> 400
	resp, err = http.Get(srv.URL + "/web/api/vless/abc")
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()
}

func TestWebUI_NodesPopulated(t *testing.T) {
	cfg := testConfig()
	uid := uint(7)
	now := time.Now()
	v4 := netip.MustParseAddr("100.64.1.5")
	v6 := netip.MustParseAddr("fd7a:1::5")
	st := &mockState{
		nodes: []types.NodeView{
			(&types.Node{
				ID:        types.NodeID(1),
				Hostname:  "node-one",
				GivenName: "node-one",
				UserID:    &uid,
				IPv4:      &v4,
				IPv6:      &v6,
				Tags:      types.Strings{"tag:server"},
				Expiry:    &now,
			}).View(),
			(&types.Node{ID: types.NodeID(2), Hostname: "node-two", GivenName: "node-two"}).View(),
		},
	}
	h := Handler(cfg, st)
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/web/api/nodes")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	nodes := body["nodes"].([]any)
	require.Len(t, nodes, 2)

	first := nodes[0].(map[string]any)
	assert.Equal(t, "node-one", first["hostname"])
	assert.Equal(t, []any{"100.64.1.5", "fd7a:1::5"}, first["ips"])
	assert.Equal(t, []any{"tag:server"}, first["tags"])
	// VLESS endpoint is derived per node.
	vless := first["vless"].(map[string]any)
	assert.NotEmpty(t, vless["uuid"])
	port := int(vless["port"].(float64))
	assert.GreaterOrEqual(t, port, 10001)
	assert.LessOrEqual(t, port, 10100)
}

func TestWebUI_VLESSSecurityVariants(t *testing.T) {
	cfg := testConfig()
	st := &mockState{}
	h := Handler(cfg, st)
	srv := httptest.NewServer(h)
	defer srv.Close()

	// default security (reality_xtls)
	resp, err := http.Get(srv.URL + "/web/api/vless/5")
	require.NoError(t, err)
	var v map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&v))
	resp.Body.Close()
	assert.Contains(t, v["uri"], "security=reality_xtls")
}

func TestWebUI_HealthFail(t *testing.T) {
	cfg := testConfig()
	st := &mockState{pingErr: assertErr("db down")}
	h := Handler(cfg, st)
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/web/api/health")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestWebUI_SPAFallback(t *testing.T) {
	cfg := testConfig()
	st := &mockState{}
	h := Handler(cfg, st)
	srv := httptest.NewServer(h)
	defer srv.Close()

	// unknown path under /web/ falls back to index.html
	resp, err := http.Get(srv.URL + "/web/some/deep/link")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/html")
}

func TestWebUI_Redirects(t *testing.T) {
	cfg := testConfig()
	st := &mockState{}
	h := Handler(cfg, st)
	srv := httptest.NewServer(h)
	defer srv.Close()

	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	for _, path := range []string{"/web", "/admin"} {
		resp, err := client.Get(srv.URL + path)
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusFound, resp.StatusCode, path)
		assert.Contains(t, resp.Header.Get("Location"), path+"/", path)
	}
}

type assertErr string

func (e assertErr) Error() string { return string(e) }
