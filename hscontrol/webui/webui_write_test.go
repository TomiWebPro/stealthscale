// Copyright (c) 2026 TomiWebPro <TomiWebPro@gmail.com>
// SPDX-License-Identifier: BSD-3-Clause

package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
	"github.com/tomiwebpro/stealthscale/hscontrol/types/change"
	"github.com/tomiwebpro/stealthscale/hscontrol/xray"
	"tailscale.com/tailcfg"
)

// writeMockState extends mockState with the optional write
// methods so tests can exercise the delegation paths (the
// production *state.State implements all of them).
type writeMockState struct {
	mockState

	expirySet       map[types.NodeID]*time.Time
	renamed         map[types.NodeID]string
	tagsSet         map[types.NodeID][]string
	routesSet       map[types.NodeID][]netip.Prefix
	expiredPAK      []uint64
	deletedPAK      []uint64
	apiKeys         []types.APIKey
	createdKeys     uint64
	expiredAK       []uint64
	destroyedAK     []uint64
	authCache       map[types.AuthID]*types.AuthRequest
	derpMaps        []*tailcfg.DERPMap
	vlessSet        map[types.NodeID]*xray.VLESSConfig
	electionMode    types.ElectionMode
	electionPrimary types.NodeID
}

func newWriteMock() *writeMockState {
	return &writeMockState{ //nolint:exhaustruct_v5 // test fixture: only fields under test
		expirySet:    map[types.NodeID]*time.Time{},
		renamed:      map[types.NodeID]string{},
		tagsSet:      map[types.NodeID][]string{},
		routesSet:    map[types.NodeID][]netip.Prefix{},
		authCache:    map[types.AuthID]*types.AuthRequest{},
		electionMode: types.ElectionModeAuto,
	}
}

func (m *writeMockState) SetNodeExpiry(nodeID types.NodeID, expiry *time.Time) (types.NodeView, change.Change, error) {
	m.expirySet[nodeID] = expiry

	return types.NodeView{}, change.Change{}, nil //nolint:exhaustruct_v5 // test fixture: only fields under test
}

func (m *writeMockState) RenameNode(nodeID types.NodeID, name string) (types.NodeView, change.Change, error) {
	m.renamed[nodeID] = name

	return types.NodeView{}, change.Change{}, nil //nolint:exhaustruct_v5 // test fixture: only fields under test
}

func (m *writeMockState) SetNodeTags(nodeID types.NodeID, tags []string) (types.NodeView, change.Change, error) {
	m.tagsSet[nodeID] = tags

	return types.NodeView{}, change.Change{}, nil //nolint:exhaustruct_v5 // test fixture: only fields under test
}

func (m *writeMockState) SetApprovedRoutes(nodeID types.NodeID, routes []netip.Prefix) (types.NodeView, change.Change, error) {
	m.routesSet[nodeID] = routes

	return types.NodeView{}, change.Change{}, nil //nolint:exhaustruct_v5 // test fixture: only fields under test
}

func (m *writeMockState) GetPreAuthKeyByID(id uint64) (*types.PreAuthKey, error) {
	for _, k := range m.keys {
		if k.ID == id {
			return &k, nil
		}
	}

	return nil, assert.AnError
}

func (m *writeMockState) ExpirePreAuthKey(id uint64) error {
	m.expiredPAK = append(m.expiredPAK, id)

	return nil
}

func (m *writeMockState) DeletePreAuthKey(id uint64) error {
	m.deletedPAK = append(m.deletedPAK, id)

	return nil
}

func (m *writeMockState) ListAPIKeys() ([]types.APIKey, error) {
	return m.apiKeys, nil
}

func (m *writeMockState) CreateAPIKey(expiration *time.Time) (string, *types.APIKey, error) {
	m.createdKeys++

	key := types.APIKey{ID: 100 + m.createdKeys, Prefix: "abcdef012345", Expiration: expiration} //nolint:exhaustruct_v5 // test fixture: only fields under test

	return "raw-key-shown-once", &key, nil
}

func (m *writeMockState) GetAPIKeyByID(id uint64) (*types.APIKey, error) {
	for _, k := range m.apiKeys {
		if k.ID == id {
			return &k, nil
		}
	}

	return nil, assert.AnError
}

func (m *writeMockState) ExpireAPIKey(key *types.APIKey) error {
	m.expiredAK = append(m.expiredAK, key.ID)
	return nil
}

func (m *writeMockState) DestroyAPIKey(key types.APIKey) error {
	m.destroyedAK = append(m.destroyedAK, key.ID)
	return nil
}

func (m *writeMockState) GetAuthCacheEntry(id types.AuthID) (*types.AuthRequest, bool) {
	req, ok := m.authCache[id]
	return req, ok
}

func (m *writeMockState) SetDERPMap(dm *tailcfg.DERPMap) {
	m.derpMaps = append(m.derpMaps, dm)
}

func (m *writeMockState) SetNodeVLESS(nodeID types.NodeID, cfg *xray.VLESSConfig) error {
	if m.vlessSet == nil {
		m.vlessSet = map[types.NodeID]*xray.VLESSConfig{}
	}
	m.vlessSet[nodeID] = cfg
	return nil
}

func (m *writeMockState) SetElectionMode(mode types.ElectionMode) {
	m.electionMode = mode
	// Mirror ElectionState.SetMode: switching to auto
	// clears any fixed primary.
	if mode == types.ElectionModeAuto {
		m.electionPrimary = 0
	}
}

func (m *writeMockState) SetElectionPrimary(nodeID types.NodeID) {
	m.electionPrimary = nodeID
}

// ElectionSnapshot reports the mock's live election state
// so tests can read back what the election write endpoints
// applied (shadows the embedded mockState stub).
func (m *writeMockState) ElectionSnapshot() map[string]any {
	primary := ""
	if m.electionPrimary != 0 {
		primary = m.electionPrimary.String()
	}
	return map[string]any{
		"mode":         string(m.electionMode),
		"primary_node": primary,
		"has_primary":  m.electionPrimary != 0,
	}
}

const testAuthID = "hskey-authreq-0123456789abcdef01234567"

func doJSON(t *testing.T, method, url string, body any) *http.Response {
	t.Helper()

	var rdr *bytes.Reader

	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)

		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}

	req, err := http.NewRequestWithContext(context.Background(), method, url, rdr)
	require.NoError(t, err)

	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	return resp
}

func TestWebUI_NodeLifecycle(t *testing.T) {
	cfg := testConfig()

	st := newWriteMock()

	st.nodes = []types.NodeView{
		(&types.Node{ID: types.NodeID(1), Hostname: "node-one", GivenName: "node-one"}).View(), //nolint:exhaustruct_v5 // test fixture: only fields under test
	}

	h := Handler(cfg, st)
	srv := httptest.NewServer(h)

	defer srv.Close()

	// expire with RFC3339 timestamp
	resp := doJSON(t, http.MethodPost, srv.URL+"/web/api/nodes/1/expire", map[string]string{"expiry": "2030-01-01T00:00:00Z"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
	require.Contains(t, st.expirySet, types.NodeID(1))
	require.NotNil(t, st.expirySet[types.NodeID(1)])

	// expire "never" -> nil expiry
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/nodes/1/expire", map[string]string{"expiry": "never"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
	assert.Nil(t, st.expirySet[types.NodeID(1)])

	// invalid expiry -> 400
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/nodes/1/expire", map[string]string{"expiry": "not-a-date"})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// rename
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/nodes/1/rename", map[string]string{"name": "renamed-node"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
	assert.Equal(t, "renamed-node", st.renamed[types.NodeID(1)])

	// rename without name -> 400
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/nodes/1/rename", map[string]string{})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// tags
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/nodes/1/tags", map[string]any{"tags": []string{"tag:server"}})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
	assert.Equal(t, []string{"tag:server"}, st.tagsSet[types.NodeID(1)])

	// tags empty -> 400
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/nodes/1/tags", map[string]any{"tags": []string{}})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// approved routes
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/nodes/1/routes", map[string]any{"routes": []string{"10.0.0.0/24", "192.168.0.0/16"}})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
	require.Len(t, st.routesSet[types.NodeID(1)], 2)

	// invalid route -> 400
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/nodes/1/routes", map[string]any{"routes": []string{"not-a-prefix"}})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// empty routes -> 200, clears the approved set: the Web UI
	// endpoint must express the same clear-all operation the CLI
	// and the v1 API support (approve_routes with an empty list),
	// so an operator who unchecks every route is not stuck with a
	// 400 and a permanently approved route.
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/nodes/1/routes", map[string]any{"routes": []string{}})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
	assert.Empty(t, st.routesSet[types.NodeID(1)])

	// unknown node -> 404
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/nodes/999/rename", map[string]string{"name": "x"})
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()

	// bad node id -> 400
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/nodes/abc/rename", map[string]string{"name": "x"})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// wrong method -> 405
	resp = doJSON(t, http.MethodGet, srv.URL+"/web/api/nodes/1/expire", nil)
	require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	resp.Body.Close()

	// unknown action -> 404
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/nodes/1/frobnicate", map[string]string{})
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()
}

func TestWebUI_NodeLifecycleStub(t *testing.T) {
	// mockState (no write methods) keeps the stub contract.
	cfg := testConfig()

	st := &mockState{ //nolint:exhaustruct_v5 // test fixture: only fields under test
		nodes: []types.NodeView{
			(&types.Node{ID: types.NodeID(1), Hostname: "node-one", GivenName: "node-one"}).View(), //nolint:exhaustruct_v5 // test fixture: only fields under test
		},
	}
	h := Handler(cfg, st)
	srv := httptest.NewServer(h)

	defer srv.Close()

	resp := doJSON(t, http.MethodPost, srv.URL+"/web/api/nodes/1/expire", map[string]string{"expiry": "never"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out map[string]any

	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	resp.Body.Close()
	assert.Contains(t, out["status"], "stub")
}

func TestWebUI_PreAuthKeyLifecycle(t *testing.T) {
	cfg := testConfig()

	st := newWriteMock()

	st.keys = []types.PreAuthKey{{ID: 5, Key: "pak-5"}} //nolint:exhaustruct_v5 // test fixture: only fields under test

	h := Handler(cfg, st)
	srv := httptest.NewServer(h)

	defer srv.Close()

	// expire
	resp := doJSON(t, http.MethodPost, srv.URL+"/web/api/preauthkeys/5/expire", map[string]string{})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
	assert.Equal(t, []uint64{5}, st.expiredPAK)

	// delete
	resp = doJSON(t, http.MethodDelete, srv.URL+"/web/api/preauthkeys/5", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
	assert.Equal(t, []uint64{5}, st.deletedPAK)

	// unknown key -> 404
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/preauthkeys/999/expire", map[string]string{})
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()

	// bad id -> 400
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/preauthkeys/abc/expire", map[string]string{})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()
}

func TestWebUI_APIKeys(t *testing.T) {
	cfg := testConfig()
	st := newWriteMock()
	created := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	ownerID := uint(7)

	st.apiKeys = []types.APIKey{
		{ID: 1, Prefix: "abcdef012345", CreatedAt: &created}, //nolint:exhaustruct_v5 // test fixture: only fields under test
		{ID: 2, Prefix: "1234567890ab", UserID: &ownerID},    //nolint:exhaustruct_v5 // test fixture: only fields under test
	}

	h := Handler(cfg, st)
	srv := httptest.NewServer(h)

	defer srv.Close()

	// list: masked prefixes, never key material
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/web/api/apikeys", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out struct {
		APIKeys []map[string]any `json:"apiKeys"`
	}

	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	resp.Body.Close()
	require.Len(t, out.APIKeys, 2)
	assert.Equal(t, "hskey-api-abcdef012345-***", out.APIKeys[0]["prefix"])
	assert.NotContains(t, out.APIKeys[0], "key")
	assert.NotContains(t, out.APIKeys[0], "hash")
	assert.InEpsilon(t, 7, out.APIKeys[1]["userID"], 0)

	// create: returns the raw key exactly once
	resp2 := doJSON(t, http.MethodPost, srv.URL+"/web/api/apikeys", map[string]string{"expiry": "2030-02-01T00:00:00Z"})
	require.Equal(t, http.StatusCreated, resp2.StatusCode)

	var createdOut struct {
		APIKey map[string]any `json:"apiKey"`
		Key    string         `json:"key"`
	}
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&createdOut))
	resp2.Body.Close()
	assert.Equal(t, "raw-key-shown-once", createdOut.Key)
	assert.NotEmpty(t, createdOut.APIKey["prefix"])

	// create without expiry -> 201, no expiration
	resp2 = doJSON(t, http.MethodPost, srv.URL+"/web/api/apikeys", map[string]string{})
	require.Equal(t, http.StatusCreated, resp2.StatusCode)
	resp2.Body.Close()

	// invalid expiry -> 400
	resp2 = doJSON(t, http.MethodPost, srv.URL+"/web/api/apikeys", map[string]string{"expiry": "nope"})
	require.Equal(t, http.StatusBadRequest, resp2.StatusCode)
	resp2.Body.Close()

	// expire
	resp2 = doJSON(t, http.MethodPost, srv.URL+"/web/api/apikeys/1/expire", map[string]string{})
	require.Equal(t, http.StatusOK, resp2.StatusCode)
	resp2.Body.Close()
	assert.Equal(t, []uint64{1}, st.expiredAK)

	// delete
	resp2 = doJSON(t, http.MethodDelete, srv.URL+"/web/api/apikeys/2", nil)
	require.Equal(t, http.StatusOK, resp2.StatusCode)
	resp2.Body.Close()
	assert.Equal(t, []uint64{2}, st.destroyedAK)

	// unknown key -> 404
	resp2 = doJSON(t, http.MethodPost, srv.URL+"/web/api/apikeys/999/expire", map[string]string{})
	require.Equal(t, http.StatusNotFound, resp2.StatusCode)
	resp2.Body.Close()

	// bad id -> 400
	resp2 = doJSON(t, http.MethodPost, srv.URL+"/web/api/apikeys/abc/expire", map[string]string{})
	require.Equal(t, http.StatusBadRequest, resp2.StatusCode)
	resp2.Body.Close()
}

func TestWebUI_AuthApproveReject(t *testing.T) {
	cfg := testConfig()

	st := newWriteMock()

	authReq := types.NewAuthRequest()
	st.authCache[types.AuthID(testAuthID)] = authReq

	h := Handler(cfg, st)
	srv := httptest.NewServer(h)

	defer srv.Close()

	// status of a pending session
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/web/api/auth/"+testAuthID, nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var status struct {
		AuthID       string `json:"authID"`
		Pending      bool   `json:"pending"`
		Registration bool   `json:"registration"`
	}

	require.NoError(t, json.NewDecoder(resp.Body).Decode(&status))
	resp.Body.Close()
	assert.Equal(t, testAuthID, status.AuthID)
	assert.True(t, status.Pending)

	// approve delivers an accepting verdict to the waiting flow
	resp2 := doJSON(t, http.MethodPost, srv.URL+"/web/api/auth/"+testAuthID+"/approve", map[string]string{})
	require.Equal(t, http.StatusOK, resp2.StatusCode)
	resp2.Body.Close()

	select {
	case v := <-authReq.WaitForAuth():
		assert.True(t, v.Accept())
		require.NoError(t, v.Err)
	default:
		t.Fatal("expected an auth verdict after approve")
	}

	// reject delivers a rejecting verdict
	rejectReq := types.NewAuthRequest()
	st.authCache[types.AuthID(testAuthID)] = rejectReq

	resp2 = doJSON(t, http.MethodPost, srv.URL+"/web/api/auth/"+testAuthID+"/reject", map[string]string{})
	require.Equal(t, http.StatusOK, resp2.StatusCode)
	resp2.Body.Close()

	select {
	case v := <-rejectReq.WaitForAuth():
		assert.False(t, v.Accept())
		require.Error(t, v.Err)
	default:
		t.Fatal("expected an auth verdict after reject")
	}

	// registration sessions cannot be decided from the WebUI: the
	// interactive auth flow (the node's auth URL) completes them,
	// and a verdict without a node would make the registering
	// client silently restart its registration
	regReq := types.NewRegisterAuthRequest(&types.RegistrationData{Hostname: "reg-host"}) //nolint:exhaustruct_v5 // test fixture: only fields under test
	st.authCache[types.AuthID(testAuthID)] = regReq

	resp2 = doJSON(t, http.MethodPost, srv.URL+"/web/api/auth/"+testAuthID+"/approve", map[string]string{})
	require.Equal(t, http.StatusConflict, resp2.StatusCode)
	resp2.Body.Close()

	resp2 = doJSON(t, http.MethodPost, srv.URL+"/web/api/auth/"+testAuthID+"/reject", map[string]string{})
	require.Equal(t, http.StatusConflict, resp2.StatusCode)
	resp2.Body.Close()

	// no verdict was delivered — the session stays pending for the
	// interactive flow to complete
	select {
	case v := <-regReq.WaitForAuth():
		t.Fatalf("registration session must not be decided from the WebUI, got verdict %+v", v)
	default:
	}

	// unknown session -> 404
	resp2 = doJSON(t, http.MethodPost, srv.URL+"/web/api/auth/hskey-authreq-ffffffffffffffffffffffff/approve", map[string]string{})
	require.Equal(t, http.StatusNotFound, resp2.StatusCode)
	resp2.Body.Close()

	// malformed auth id -> 400
	resp2 = doJSON(t, http.MethodPost, srv.URL+"/web/api/auth/not-an-auth-id/approve", map[string]string{})
	require.Equal(t, http.StatusBadRequest, resp2.StatusCode)
	resp2.Body.Close()

	// missing id -> 400
	resp2 = doJSON(t, http.MethodPost, srv.URL+"/web/api/auth//approve", map[string]string{})
	require.Equal(t, http.StatusBadRequest, resp2.StatusCode)
	resp2.Body.Close()
}

// TestWebUI_AuthDoubleFinishIsNoop pins the double-finish
// failure mode of the Web UI auth approve/reject endpoints:
// an SSH-check session that has already been decided — a
// double-click on Approve, the same session approved from
// two browser tabs, or an approve-then-reject race — must be
// a no-op, never a second verdict. [types.AuthRequest.FinishAuth]
// guards this with its closed flag: without it the second
// call would send on the closed verdict channel and panic the
// handler (net/http answers a panicking handler by closing
// the connection, so the operator's second click would surface
// as a transport error), and a racing reject could flip an
// already-delivered accept for the parked SSH-check follow-up
// (hscontrol sshActionFollowUp consumes exactly one verdict).
func TestWebUI_AuthDoubleFinishIsNoop(t *testing.T) {
	cfg := testConfig()

	st := newWriteMock()

	// SSH-check sessions are the sessions the Web UI decides;
	// registration sessions are refused with 409 (pinned in
	// TestWebUI_AuthApproveReject).
	authReq := types.NewSSHCheckAuthRequest(types.NodeID(1), types.NodeID(2))
	st.authCache[types.AuthID(testAuthID)] = authReq

	h := Handler(cfg, st)
	srv := httptest.NewServer(h)

	defer srv.Close()

	// First approve: one accepting verdict is delivered.
	resp := doJSON(t, http.MethodPost, srv.URL+"/web/api/auth/"+testAuthID+"/approve", map[string]string{})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	select {
	case v := <-authReq.WaitForAuth():
		assert.True(t, v.Accept())
		require.NoError(t, v.Err)
	case <-time.After(5 * time.Second):
		t.Fatal("expected the first approve to deliver a verdict")
	}

	// Double-click approve, then a racing reject: both must be
	// no-ops. The handler answers 200 (the session is already
	// finished, but the request itself is valid) — a missing
	// closed-guard panics on send-to-closed-channel and the
	// client sees a dropped connection instead.
	for _, action := range []string{"approve", "reject"} {
		resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/auth/"+testAuthID+"/"+action, map[string]string{})
		require.Equal(t, http.StatusOK, resp.StatusCode, "%s after a finished session must be a no-op", action)
		resp.Body.Close()
	}

	// The verdict channel is closed after the single
	// verdict, so reads return promptly instead of
	// parking. (A closed channel yields the zero-value
	// AuthVerdict, whose Accept() is vacuously true —
	// the real guard is the 200 no-op above: a missing
	// closed-guard would have panicked on the second
	// FinishAuth and dropped these connections.)
	select {
	case <-authReq.WaitForAuth():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("verdict channel must be closed after the first verdict")
	}
}

func TestWebUI_PolicyCheck(t *testing.T) {
	cfg := testConfig()
	st := newWriteMock()
	st.users = []types.User{{Name: "alice"}}       //nolint:exhaustruct_v5 // test fixture: only fields under test
	st.policy = &types.Policy{Data: `{"acls":[]}`} //nolint:exhaustruct_v5 // test fixture: only fields under test
	h := Handler(cfg, st)
	srv := httptest.NewServer(h)

	defer srv.Close()

	// valid policy -> 200
	resp := doJSON(t, http.MethodPost, srv.URL+"/web/api/policy/check", map[string]string{"policy": `{"acls":[]}`})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out map[string]any

	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	resp.Body.Close()
	assert.Equal(t, true, out["valid"])

	// invalid policy -> 400
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/policy/check", map[string]string{"policy": "{not json"})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// missing policy -> 400
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/policy/check", map[string]string{})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// GET on the check path is not registered -> 404/405 (not a valid op)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/web/api/policy/check", nil)
	require.NoError(t, err)
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp2.Body.Close()
	assert.NotEqual(t, http.StatusOK, resp2.StatusCode)
}

func TestWebUI_CreateUser(t *testing.T) {
	cfg := testConfig()
	st := &mockState{}
	h := Handler(cfg, st)
	srv := httptest.NewServer(h)
	defer srv.Close()

	// valid
	body, _ := json.Marshal(map[string]string{"name": "bob", "email": "bob@example.com"})
	resp, err := http.Post(srv.URL+"/web/api/users", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	assert.NotNil(t, out["user"])

	// missing name -> 400
	body, _ = json.Marshal(map[string]string{"email": "bob@example.com"})
	resp, err = http.Post(srv.URL+"/web/api/users", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	// invalid JSON -> 400
	resp, err = http.Post(srv.URL+"/web/api/users", "application/json", bytes.NewReader([]byte("{invalid")))
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestWebUI_CreatePreAuthKey(t *testing.T) {
	cfg := testConfig()
	st := &mockState{}
	h := Handler(cfg, st)
	srv := httptest.NewServer(h)
	defer srv.Close()

	body, _ := json.Marshal(map[string]any{"reusable": true, "ephemeral": false, "aclTags": []string{"tag:server"}})
	resp, err := http.Post(srv.URL+"/web/api/preauthkeys", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
}

func TestWebUI_SetPolicy(t *testing.T) {
	cfg := testConfig()
	st := &mockState{}
	h := Handler(cfg, st)
	srv := httptest.NewServer(h)
	defer srv.Close()

	// PUT via POST also allowed
	pol := `{"acls":[]}`
	body, _ := json.Marshal(map[string]string{"policy": pol})
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/web/api/policy", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// empty policy -> 400
	req, _ = http.NewRequest(http.MethodPut, srv.URL+"/web/api/policy", bytes.NewReader([]byte(`{"policy":""}`)))
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestWebUI_DeleteNode(t *testing.T) {
	cfg := testConfig()
	st := &mockState{}
	h := Handler(cfg, st)
	srv := httptest.NewServer(h)
	defer srv.Close()

	// DELETE with id
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/web/api/nodes/123", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	assert.Equal(t, "123", out["nodeID"])

	// missing id -> 400
	req, _ = http.NewRequest(http.MethodDelete, srv.URL+"/web/api/nodes/", nil)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	// invalid id -> 400
	req, _ = http.NewRequest(http.MethodDelete, srv.URL+"/web/api/nodes/abc", nil)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestWebUI_DERPLifecycle(t *testing.T) {
	cfg := testConfig()
	st := newWriteMock()
	h := Handler(cfg, st)
	srv := httptest.NewServer(h)
	defer srv.Close()

	validRegion := map[string]any{
		"regionID": 1,
		"nodes": []any{
			map[string]any{"name": "1a", "regionID": 1, "hostName": "derp.example.com"},
		},
	}

	// POST with the {"derpMap": ...} wrapper (the shape GET /derp returns)
	resp := doJSON(t, http.MethodPost, srv.URL+"/web/api/derp", map[string]any{"derpMap": map[string]any{"regions": map[string]any{"1": validRegion}}})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out map[string]any

	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	resp.Body.Close()
	assert.Equal(t, "derp map applied", out["status"])
	require.Len(t, st.derpMaps, 1)
	require.Len(t, st.derpMaps[0].Regions, 1)
	require.NotNil(t, st.derpMaps[0].Regions[1])
	// Response reports the stealth gate so the operator sees whether
	// the map is actually offered to clients right now.
	assert.Contains(t, out, "stealth_satisfied")
	assert.Contains(t, out, "shouldIncludeDERP")

	// POST a bare DERPMap object (stock derp.yaml-shaped JSON)
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/derp", map[string]any{"regions": map[string]any{"1": validRegion}})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
	require.Len(t, st.derpMaps, 2)

	// empty region set is valid — it disables DERP relays
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/derp", map[string]any{"derpMap": map[string]any{"regions": map[string]any{}}})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
	require.Len(t, st.derpMaps, 3)
	assert.Empty(t, st.derpMaps[2].Regions)

	// invalid JSON -> 400
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/derp", map[string]any{"derpMap": "not-a-map"})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// region key / RegionID mismatch -> 400
	badRegion := map[string]any{
		"regionID": 999,
		"nodes":    []any{map[string]any{"name": "5a", "regionID": 5, "hostName": "derp.example.com"}},
	}
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/derp", map[string]any{"derpMap": map[string]any{"regions": map[string]any{"5": badRegion}}})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// non-positive region id -> 400
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/derp", map[string]any{"derpMap": map[string]any{"regions": map[string]any{"0": validRegion}}})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// node RegionID does not match its region -> 400
	wrongNodeRegion := map[string]any{
		"regionID": 1,
		"nodes":    []any{map[string]any{"name": "1a", "regionID": 2, "hostName": "derp.example.com"}},
	}
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/derp", map[string]any{"derpMap": map[string]any{"regions": map[string]any{"1": wrongNodeRegion}}})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// node without a name -> 400
	namelessNode := map[string]any{
		"regionID": 1,
		"nodes":    []any{map[string]any{"regionID": 1, "hostName": "derp.example.com"}},
	}
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/derp", map[string]any{"derpMap": map[string]any{"regions": map[string]any{"1": namelessNode}}})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// node without hostname or forced IP -> 400
	unreachableNode := map[string]any{
		"regionID": 1,
		"nodes":    []any{map[string]any{"name": "1a", "regionID": 1}},
	}
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/derp", map[string]any{"derpMap": map[string]any{"regions": map[string]any{"1": unreachableNode}}})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// GET keeps the read-only shape
	resp = doJSON(t, http.MethodGet, srv.URL+"/web/api/derp", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got map[string]any

	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	resp.Body.Close()
	assert.Contains(t, got, "derpMap")
	assert.Contains(t, got, "stealth_satisfied")

	// wrong method -> 405
	resp = doJSON(t, http.MethodPut, srv.URL+"/web/api/derp", map[string]any{"derpMap": map[string]any{}})
	require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	resp.Body.Close()

	// stub path: a State without SetDERPMap keeps the stub contract
	stStub := &mockState{}
	hStub := Handler(cfg, stStub)
	srvStub := httptest.NewServer(hStub)
	defer srvStub.Close()

	resp = doJSON(t, http.MethodPost, srvStub.URL+"/web/api/derp", map[string]any{"derpMap": map[string]any{"regions": map[string]any{"1": validRegion}}})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var stubOut map[string]any

	require.NoError(t, json.NewDecoder(resp.Body).Decode(&stubOut))
	resp.Body.Close()
	assert.Contains(t, stubOut["status"], "stub")
}

func TestWebUI_VLESSSettings(t *testing.T) {
	cfg := testConfig()
	st := newWriteMock()
	st.nodes = []types.NodeView{
		(&types.Node{ID: types.NodeID(1), Hostname: "node-one", GivenName: "node-one"}).View(), //nolint:exhaustruct_v5 // test fixture: only fields under test
	}
	h := Handler(cfg, st)
	srv := httptest.NewServer(h)
	defer srv.Close()

	// valid settings delegate to SetNodeVLESS and echo the endpoint
	resp := doJSON(t, http.MethodPost, srv.URL+"/web/api/vless/1", map[string]any{"address": "10.0.0.1", "port": 10001, "security": "reality_xtls"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out map[string]any

	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	resp.Body.Close()
	assert.Equal(t, "vless endpoint set", out["status"])
	require.Contains(t, st.vlessSet, types.NodeID(1))
	assert.Equal(t, "10.0.0.1", st.vlessSet[types.NodeID(1)].Address)
	assert.Equal(t, 10001, st.vlessSet[types.NodeID(1)].Port)
	assert.Equal(t, "reality_xtls", st.vlessSet[types.NodeID(1)].Security)
	// The response carries the derived UUID and the reusable URI.
	ep := out["endpoint"].(map[string]any)
	assert.NotEmpty(t, ep["uuid"])
	assert.Contains(t, ep["uri"], "vless://")
	assert.Contains(t, ep["uri"], "security=reality_xtls")

	// "reality" is accepted as the reality_xtls alias
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/vless/1", map[string]any{"address": "10.0.0.1", "port": 10001, "security": "reality"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
	assert.Equal(t, "reality_xtls", st.vlessSet[types.NodeID(1)].Security)

	// empty security defaults to reality_xtls
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/vless/1", map[string]any{"address": "10.0.0.1", "port": 10001})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
	assert.Equal(t, "reality_xtls", st.vlessSet[types.NodeID(1)].Security)

	// invalid security variant -> 400
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/vless/1", map[string]any{"address": "10.0.0.1", "port": 10001, "security": "carrier-pigeon"})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// port out of range -> 400
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/vless/1", map[string]any{"address": "10.0.0.1", "port": 65536, "security": "reality_xtls"})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/vless/1", map[string]any{"address": "10.0.0.1", "port": 0, "security": "reality_xtls"})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// empty address -> 400
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/vless/1", map[string]any{"port": 10001, "security": "reality_xtls"})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// unknown node -> 404
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/vless/999", map[string]any{"address": "10.0.0.1", "port": 10001, "security": "reality_xtls"})
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()

	// invalid JSON -> 400
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/vless/1", map[string]any{"port": "not-a-number"})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// bad node id -> 400
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/vless/abc", map[string]any{"address": "10.0.0.1", "port": 10001})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// GET keeps the read-only derivation
	resp = doJSON(t, http.MethodGet, srv.URL+"/web/api/vless/1", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got map[string]any

	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	resp.Body.Close()
	assert.Contains(t, got["uri"], "vless://")

	// wrong method -> 405
	resp = doJSON(t, http.MethodDelete, srv.URL+"/web/api/vless/1", nil)
	require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	resp.Body.Close()

	// stub path: a State without SetNodeVLESS keeps the stub contract
	stStub := &mockState{
		nodes: []types.NodeView{
			(&types.Node{ID: types.NodeID(1), Hostname: "node-one", GivenName: "node-one"}).View(), //nolint:exhaustruct_v5 // test fixture: only fields under test
		},
	}
	hStub := Handler(cfg, stStub)
	srvStub := httptest.NewServer(hStub)
	defer srvStub.Close()

	resp = doJSON(t, http.MethodPost, srvStub.URL+"/web/api/vless/1", map[string]any{"address": "10.0.0.1", "port": 10001, "security": "reality_xtls"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var stubOut map[string]any

	require.NoError(t, json.NewDecoder(resp.Body).Decode(&stubOut))
	resp.Body.Close()
	assert.Contains(t, stubOut["status"], "stub")
}

// TestWebUI_ElectionMode pins the election mode switch
// (goal #4: coordinator election must be configurable
// from the Web UI). POST /web/api/election validates
// the "mode" field and delegates to SetElectionMode /
// SetElectionPrimary — the regression guard for the
// mode-decoded-then-ignored bug (the handler used to
// discard the decoded mode and only ever set the
// primary, so manual mode was unreachable).
func TestWebUI_ElectionMode(t *testing.T) {
	cfg := testConfig()
	st := newWriteMock()
	h := Handler(cfg, st)
	srv := httptest.NewServer(h)
	defer srv.Close()

	// GET returns the live snapshot: auto mode, no
	// fixed primary.
	resp := doJSON(t, http.MethodGet, srv.URL+"/web/api/election", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var snap map[string]any

	require.NoError(t, json.NewDecoder(resp.Body).Decode(&snap))
	resp.Body.Close()
	assert.Equal(t, "auto", snap["mode"])
	assert.Equal(t, false, snap["has_primary"])

	// POST mode + node_id switches to manual and sets
	// the fixed primary in one step.
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/election", map[string]any{"mode": "manual", "node_id": 42})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out map[string]any

	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	resp.Body.Close()
	assert.Equal(t, types.ElectionModeManual, st.electionMode)
	assert.Equal(t, types.NodeID(42), st.electionPrimary)
	assert.Equal(t, "manual", out["mode"])
	assert.Equal(t, true, out["has_primary"])
	assert.Equal(t, "42", out["primary_node"])

	// POST mode only keeps the existing primary.
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/election", map[string]any{"mode": "manual"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
	assert.Equal(t, types.NodeID(42), st.electionPrimary)

	// Switching back to auto clears the fixed primary.
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/election", map[string]any{"mode": "auto"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	resp.Body.Close()
	assert.Equal(t, types.ElectionModeAuto, st.electionMode)
	assert.Equal(t, types.NodeID(0), st.electionPrimary)
	assert.Equal(t, false, out["has_primary"])

	// Legacy form: node_id without a mode only sets
	// the primary, the mode is untouched.
	st.electionMode = types.ElectionModeManual
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/election", map[string]any{"node_id": 7})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
	assert.Equal(t, types.ElectionModeManual, st.electionMode)
	assert.Equal(t, types.NodeID(7), st.electionPrimary)

	// Empty body keeps the legacy clear-primary
	// behaviour.
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/election", map[string]any{})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
	assert.Equal(t, types.NodeID(0), st.electionPrimary)

	// Invalid mode -> 400, no state change.
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/election", map[string]any{"mode": "bogus", "node_id": 9})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()
	assert.Equal(t, types.ElectionModeManual, st.electionMode)
	assert.Equal(t, types.NodeID(0), st.electionPrimary)

	// Invalid JSON -> 400.
	resp = doJSON(t, http.MethodPost, srv.URL+"/web/api/election", map[string]any{"mode": 42})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// Wrong method -> 405.
	resp = doJSON(t, http.MethodDelete, srv.URL+"/web/api/election", nil)
	require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	resp.Body.Close()

	// Stub path: a State without the write methods
	// keeps the stub contract.
	stStub := &mockState{}
	hStub := Handler(cfg, stStub)
	srvStub := httptest.NewServer(hStub)
	defer srvStub.Close()

	resp = doJSON(t, http.MethodPost, srvStub.URL+"/web/api/election", map[string]any{"mode": "manual", "node_id": 1})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var stubOut map[string]any

	require.NoError(t, json.NewDecoder(resp.Body).Decode(&stubOut))
	resp.Body.Close()
	assert.Contains(t, stubOut["status"], "stub")
}
