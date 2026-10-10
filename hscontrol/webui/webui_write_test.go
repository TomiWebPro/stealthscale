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
)

// writeMockState extends mockState with the optional write
// methods so tests can exercise the delegation paths (the
// production *state.State implements all of them).
type writeMockState struct {
	mockState

	expirySet   map[types.NodeID]*time.Time
	renamed     map[types.NodeID]string
	tagsSet     map[types.NodeID][]string
	routesSet   map[types.NodeID][]netip.Prefix
	expiredPAK  []uint64
	deletedPAK  []uint64
	apiKeys     []types.APIKey
	createdKeys uint64
	expiredAK   []uint64
	destroyedAK []uint64
	authCache   map[types.AuthID]*types.AuthRequest
}

func newWriteMock() *writeMockState {
	return &writeMockState{ //nolint:exhaustruct_v5 // test fixture: only fields under test
		expirySet: map[types.NodeID]*time.Time{},
		renamed:   map[types.NodeID]string{},
		tagsSet:   map[types.NodeID][]string{},
		routesSet: map[types.NodeID][]netip.Prefix{},
		authCache: map[types.AuthID]*types.AuthRequest{},
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
