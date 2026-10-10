// Copyright (c) 2026 TomiWebPro <TomiWebPro@gmail.com>
// SPDX-License-Identifier: BSD-3-Clause

package servertest

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
	"github.com/tomiwebpro/stealthscale/hscontrol/webui"
)

// TestWebUI_ManagementDelegatesToRealState pins the WebUI
// management-parity endpoints against a real *state.State: the
// write delegation interfaces must be satisfied (no stub
// fallback) and the change must land in the live state.
func TestWebUI_ManagementDelegatesToRealState(t *testing.T) {
	srv := NewServer(t)
	defer srv.Close()

	st := srv.State()

	user := srv.CreateUser(t, "webui-mgmt")

	node := srv.CreateRegisteredNode(t, user, "webui-mgmt-node")

	h := webui.Handler(&types.Config{}, st) //nolint:exhaustruct_v5 // test fixture: zero config

	ts := httptest.NewServer(h)
	defer ts.Close()

	doReq := func(method, path string, body any) (int, map[string]any) {
		var rdr *bytes.Reader

		if body != nil {
			b, err := json.Marshal(body)
			require.NoError(t, err)

			rdr = bytes.NewReader(b)
		} else {
			rdr = bytes.NewReader(nil)
		}

		req, err := http.NewRequestWithContext(context.Background(), method, ts.URL+path, rdr)
		require.NoError(t, err)

		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)

		defer resp.Body.Close()

		out := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&out)

		return resp.StatusCode, out
	}

	doGet := func(path string) *http.Response {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+path, nil)
		require.NoError(t, err)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)

		return resp
	}

	nodePath := "/web/api/nodes/" + node.ID().String()

	// Node expire: the change is visible on the live node view.
	expiry := time.Now().Add(time.Hour).UTC().Truncate(time.Second)

	code, out := doReq(http.MethodPost, nodePath+"/expire", map[string]string{"expiry": expiry.Format(time.RFC3339)})
	require.Equal(t, http.StatusOK, code, out)
	assert.NotContains(t, out["status"], "stub")

	nv, ok := st.GetNodeByID(node.ID())
	require.True(t, ok)
	require.True(t, nv.Expiry().Valid())
	assert.True(t, nv.Expiry().Get().Equal(expiry))

	// Node rename.
	code, out = doReq(http.MethodPost, nodePath+"/rename", map[string]string{"name": "webui-mgmt-renamed"})
	require.Equal(t, http.StatusOK, code, out)
	assert.NotContains(t, out["status"], "stub")

	nv, ok = st.GetNodeByID(node.ID())
	require.True(t, ok)
	assert.Equal(t, "webui-mgmt-renamed", nv.GivenName())

	// Node tags (tag must exist in the live policy).
	pol := `{"groups":{"group:webui-mgmt":["webui-mgmt@example.com"]},"tagOwners":{"tag:webui-mgmt":["group:webui-mgmt"]},"acls":[]}`

	_, err := st.SetPolicy([]byte(pol))
	require.NoError(t, err)

	code, out = doReq(http.MethodPost, nodePath+"/tags", map[string]any{"tags": []string{"tag:webui-mgmt"}})
	require.Equal(t, http.StatusOK, code, out)
	assert.NotContains(t, out["status"], "stub")

	nv, ok = st.GetNodeByID(node.ID())
	require.True(t, ok)
	assert.Equal(t, []string{"tag:webui-mgmt"}, nv.Tags().AsSlice())

	// Node approved routes.
	code, out = doReq(http.MethodPost, nodePath+"/routes", map[string]any{"routes": []string{"10.64.0.0/24"}})
	require.Equal(t, http.StatusOK, code, out)
	assert.NotContains(t, out["status"], "stub")

	nv, ok = st.GetNodeByID(node.ID())
	require.True(t, ok)
	require.Len(t, nv.ApprovedRoutes().AsSlice(), 1)

	// Empty route list clears the approved set — the Web UI
	// endpoint must express the same clear-all operation the
	// CLI (`stscale nodes approve-routes -r ""`) and the v1
	// API support, so an operator who unchecks every route in
	// the UI is not stuck with a 400 and a permanently
	// approved route.
	code, out = doReq(http.MethodPost, nodePath+"/routes", map[string]any{"routes": []string{}})
	require.Equal(t, http.StatusOK, code, out)
	assert.NotContains(t, out["status"], "stub")

	nv, ok = st.GetNodeByID(node.ID())
	require.True(t, ok)
	assert.Empty(t, nv.ApprovedRoutes().AsSlice(), "approved routes must be cleared by an empty list")

	// Pre-auth key expire, then delete. Keys are tagged or
	// user-owned (tags-XOR-user identity rule).
	userID := types.UserID(user.ID)

	pak, err := st.CreatePreAuthKey(&userID, true, false, nil, nil)
	require.NoError(t, err)

	pakPath := "/web/api/preauthkeys/" + strconv.FormatUint(pak.ID, 10)

	code, out = doReq(http.MethodPost, pakPath+"/expire", map[string]string{})
	require.Equal(t, http.StatusOK, code, out)
	assert.NotContains(t, out["status"], "stub")

	code, out = doReq(http.MethodDelete, pakPath, nil)
	require.Equal(t, http.StatusOK, code, out)
	assert.NotContains(t, out["status"], "stub")

	_, err = st.GetPreAuthKeyByID(pak.ID)
	require.Error(t, err, "deleted pre-auth key must be gone")

	// API key create (raw key returned once and valid), expire, destroy.
	code, out = doReq(http.MethodPost, "/web/api/apikeys", map[string]string{})
	require.Equal(t, http.StatusCreated, code, out)
	assert.NotContains(t, out["apiKey"], "stub")

	rawKey, _ := out["key"].(string)
	require.NotEmpty(t, rawKey)

	valid, err := st.ValidateAPIKey(rawKey)
	require.NoError(t, err)
	require.True(t, valid, "created API key must authenticate")

	apiKeyID, _ := out["apiKey"].(map[string]any)["id"].(float64)
	require.NotZero(t, apiKeyID)

	apiKeyPath := "/web/api/apikeys/" + strconv.FormatUint(uint64(apiKeyID), 10)

	code, out = doReq(http.MethodPost, apiKeyPath+"/expire", map[string]string{})
	require.Equal(t, http.StatusOK, code, out)
	assert.NotContains(t, out["status"], "stub")

	valid, err = st.ValidateAPIKey(rawKey)
	require.NoError(t, err)
	assert.False(t, valid, "expired API key must not authenticate")

	code, out = doReq(http.MethodDelete, apiKeyPath, nil)
	require.Equal(t, http.StatusOK, code, out)
	assert.NotContains(t, out["status"], "stub")

	// API key listing exposes masked prefixes only.
	resp := doGet("/web/api/apikeys")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var list struct {
		APIKeys []map[string]any `json:"apiKeys"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&list))
	resp.Body.Close()

	for _, k := range list.APIKeys {
		assert.NotContains(t, k, "key")
		assert.NotContains(t, k, "hash")
	}

	// Auth session status + approve delivers the verdict.
	authReq := types.NewAuthRequest()
	authID := types.MustAuthID()

	st.SetAuthCacheEntry(authID, authReq)

	resp = doGet("/web/api/auth/" + authID.String())
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var status struct {
		Pending bool `json:"pending"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&status))
	resp.Body.Close()

	assert.True(t, status.Pending)

	code, out = doReq(http.MethodPost, "/web/api/auth/"+authID.String()+"/approve", map[string]string{})
	require.Equal(t, http.StatusOK, code, out)
	assert.NotContains(t, out["status"], "stub")

	select {
	case v := <-authReq.WaitForAuth():
		assert.True(t, v.Accept())
	default:
		t.Fatal("expected an accepting verdict after WebUI approve")
	}

	// Registration sessions are refused: the interactive auth
	// flow (the node's auth URL) completes them — a verdict
	// here would carry no node and the registering client
	// would silently restart its registration.
	regReq := types.NewRegisterAuthRequest(&types.RegistrationData{Hostname: "reg-session"}) //nolint:exhaustruct_v5 // test fixture: only fields under test
	regID := types.MustAuthID()

	st.SetAuthCacheEntry(regID, regReq)

	code, out = doReq(http.MethodPost, "/web/api/auth/"+regID.String()+"/approve", map[string]string{})
	require.Equal(t, http.StatusConflict, code, out)

	code, out = doReq(http.MethodPost, "/web/api/auth/"+regID.String()+"/reject", map[string]string{})
	require.Equal(t, http.StatusConflict, code, out)

	select {
	case v := <-regReq.WaitForAuth():
		t.Fatalf("registration session must not be decided from the WebUI, got verdict %+v", v)
	default:
	}

	// Policy check validates against live users/nodes without persisting.
	code, out = doReq(http.MethodPost, "/web/api/policy/check", map[string]string{"policy": pol})
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, true, out["valid"])

	code, _ = doReq(http.MethodPost, "/web/api/policy/check", map[string]string{"policy": "{broken"})
	assert.Equal(t, http.StatusBadRequest, code)
}

// TestWebUI_ElectionDelegatesToRealState pins the election
// surface against a real *state.State: the mode switch and
// primary delegation interfaces must be satisfied (no stub
// fallback) and the switch must land in the live election
// snapshot — including the auto-clears-primary invariant and
// the "mode switch is the final word" ordering.
func TestWebUI_ElectionDelegatesToRealState(t *testing.T) {
	srv := NewServer(t)
	defer srv.Close()

	st := srv.State()

	user := srv.CreateUser(t, "webui-election")

	node := srv.CreateRegisteredNode(t, user, "webui-election-node")

	h := webui.Handler(&types.Config{}, st) //nolint:exhaustruct_v5 // test fixture: zero config

	ts := httptest.NewServer(h)
	defer ts.Close()

	doReq := func(method, path string, body any) (int, map[string]any) {
		var rdr *bytes.Reader

		if body != nil {
			b, err := json.Marshal(body)
			require.NoError(t, err)

			rdr = bytes.NewReader(b)
		} else {
			rdr = bytes.NewReader(nil)
		}

		req, err := http.NewRequestWithContext(context.Background(), method, ts.URL+path, rdr)
		require.NoError(t, err)

		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)

		defer resp.Body.Close()

		out := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&out)

		return resp.StatusCode, out
	}

	snapshot := func() map[string]any {
		return st.ElectionSnapshot()
	}

	// Fresh state: automatic mode, no fixed primary.
	code, out := doReq(http.MethodGet, "/web/api/election", nil)
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, string(types.ElectionModeAuto), out["mode"])
	assert.Equal(t, false, out["has_primary"])

	// Switch to manual and set the primary in one step.
	code, out = doReq(http.MethodPost, "/web/api/election", map[string]any{
		"mode":    string(types.ElectionModeManual),
		"node_id": uint64(node.ID()),
	})
	require.Equal(t, http.StatusOK, code, out)
	assert.NotContains(t, out["status"], "stub")
	assert.Equal(t, string(types.ElectionModeManual), out["mode"])
	assert.Equal(t, node.ID().String(), out["primary_node"])
	assert.Equal(t, true, out["has_primary"])

	snap := snapshot()
	assert.Equal(t, string(types.ElectionModeManual), snap["mode"])
	assert.Equal(t, node.ID().String(), snap["primary_node"])
	assert.Equal(t, true, snap["has_primary"])

	// Switching back to auto clears the fixed primary: in auto
	// mode any node may become coordinator, so a stored primary
	// would be stale.
	code, out = doReq(http.MethodPost, "/web/api/election", map[string]any{
		"mode": string(types.ElectionModeAuto),
	})
	require.Equal(t, http.StatusOK, code, out)
	assert.NotContains(t, out["status"], "stub")
	assert.Equal(t, string(types.ElectionModeAuto), out["mode"])
	assert.Equal(t, false, out["has_primary"])
	assert.Equal(t, string(types.ElectionModeAuto), snapshot()["mode"])
	assert.Equal(t, false, snapshot()["has_primary"])

	// The mode switch is the final word: a node_id sent
	// together with mode "auto" is applied first and then
	// cleared by the switch, never leaving a stale primary.
	code, out = doReq(http.MethodPost, "/web/api/election", map[string]any{
		"mode":    string(types.ElectionModeAuto),
		"node_id": uint64(node.ID()),
	})
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, false, snapshot()["has_primary"], "auto switch must clear a node_id sent in the same request")

	// Legacy form (node_id only, no mode): set the primary,
	// leave the mode untouched.
	code, out = doReq(http.MethodPost, "/web/api/election", map[string]any{
		"node_id": uint64(node.ID()),
	})
	require.Equal(t, http.StatusOK, code, out)
	assert.NotContains(t, out["status"], "stub")
	assert.Equal(t, node.ID().String(), snapshot()["primary_node"])
	assert.Equal(t, true, snapshot()["has_primary"])
	assert.Equal(t, string(types.ElectionModeAuto), snapshot()["mode"])

	// Unknown mode -> 400 with no state change.
	code, _ = doReq(http.MethodPost, "/web/api/election", map[string]any{
		"mode":    "carrier-pigeon",
		"node_id": uint64(node.ID()),
	})
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, string(types.ElectionModeAuto), snapshot()["mode"])

	// Invalid JSON -> 400.
	code, _ = doReq(http.MethodPost, "/web/api/election", map[string]any{"mode": 42})
	assert.Equal(t, http.StatusBadRequest, code)

	// Wrong method -> 405.
	code, _ = doReq(http.MethodPut, "/web/api/election", map[string]any{})
	assert.Equal(t, http.StatusMethodNotAllowed, code)
}

// TestWebUI_DERPRuntimeApplyDelegatesToRealState pins the
// runtime DERP map apply against a real *state.State: the
// POST must delegate to SetDERPMapRuntime (no stub
// fallback), the map must land in the live state, the
// map must be reported as operator-applied (runtime) so
// the config-derived auto-updater leaves it alone, and
// GET must report the same provenance.
func TestWebUI_DERPRuntimeApplyDelegatesToRealState(t *testing.T) {
	srv := NewServer(t)
	defer srv.Close()

	st := srv.State()

	h := webui.Handler(&types.Config{}, st) //nolint:exhaustruct_v5 // test fixture: zero config

	ts := httptest.NewServer(h)
	defer ts.Close()

	doReq := func(method, path string, body any) (int, map[string]any) {
		var rdr *bytes.Reader

		if body != nil {
			b, err := json.Marshal(body)
			require.NoError(t, err)

			rdr = bytes.NewReader(b)
		} else {
			rdr = bytes.NewReader(nil)
		}

		req, err := http.NewRequestWithContext(context.Background(), method, ts.URL+path, rdr)
		require.NoError(t, err)

		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)

		defer resp.Body.Close()

		out := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&out)

		return resp.StatusCode, out
	}

	region := map[string]any{
		"regionID": 910,
		"nodes": []any{
			map[string]any{"name": "910a", "regionID": 910, "hostName": "derp910.example.com"},
		},
	}

	// Apply a runtime map: the delegation must reach the
	// real state (no stub) and the map must land live.
	code, out := doReq(http.MethodPost, "/web/api/derp", map[string]any{"derpMap": map[string]any{"regions": map[string]any{"910": region}}})
	require.Equal(t, http.StatusOK, code, out)
	assert.NotContains(t, out["status"], "stub")
	assert.Equal(t, true, out["runtime"])

	require.True(t, st.DERPMapIsRuntime(), "an operator-applied map must be reported as runtime")

	regions := st.DERPMap().AsStruct().Regions
	require.Len(t, regions, 1)
	require.NotNil(t, regions[910])
	assert.Equal(t, "derp910.example.com", regions[910].Nodes[0].HostName)

	// GET reports the same provenance.
	code, out = doReq(http.MethodGet, "/web/api/derp", nil)
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, true, out["runtime"])

	// Invalid map -> 400, provenance untouched.
	code, _ = doReq(http.MethodPost, "/web/api/derp", map[string]any{"derpMap": map[string]any{"regions": map[string]any{"0": region}}})
	assert.Equal(t, http.StatusBadRequest, code)
	assert.True(t, st.DERPMapIsRuntime())
}

// TestWebUI_AuthFinishDelegatesToRealState pins the WebUI
// auth session endpoints (status / approve / reject) against
// a real *state.State: the authCacheReader delegation must be
// satisfied (no stub fallback) and a decided SSH-check
// session must actually deliver its verdict to the waiting
// client. The failure mode is silent: if the type-asserted
// delegation stops matching, the handlers answer 200 with a
// "stub" status and no verdict is ever delivered, leaving the
// SSH check hanging until its own timeout.
func TestWebUI_AuthFinishDelegatesToRealState(t *testing.T) {
	srv := NewServer(t)
	defer srv.Close()

	st := srv.State()

	h := webui.Handler(&types.Config{}, st) //nolint:exhaustruct_v5 // test fixture: zero config

	ts := httptest.NewServer(h)
	defer ts.Close()

	doReq := func(method, path string, body any) (int, map[string]any) {
		var rdr *bytes.Reader

		if body != nil {
			b, err := json.Marshal(body)
			require.NoError(t, err)

			rdr = bytes.NewReader(b)
		} else {
			rdr = bytes.NewReader(nil)
		}

		req, err := http.NewRequestWithContext(context.Background(), method, ts.URL+path, rdr)
		require.NoError(t, err)

		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)

		defer resp.Body.Close()

		out := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&out)

		return resp.StatusCode, out
	}

	// verdictOf reads the verdict a decided session delivers
	// to its waiting client, failing the test when nothing
	// arrives (the delegation-drift failure mode).
	verdictOf := func(authReq *types.AuthRequest) types.AuthVerdict {
		t.Helper()

		select {
		case v := <-authReq.WaitForAuth():
			return v
		case <-time.After(5 * time.Second):
			t.Fatal("no verdict delivered to the waiting client")
			return types.AuthVerdict{Err: nil, Node: types.NodeView{}}
		}
	}

	user := srv.CreateUser(t, "webui-auth")

	src := srv.CreateRegisteredNode(t, user, "sshcheck-src")
	dst := srv.CreateRegisteredNode(t, user, "sshcheck-dst")

	// Status of a pending SSH-check session (no stub field).
	sshAuthID := types.MustAuthID()
	sshReq := types.NewSSHCheckAuthRequest(src.ID(), dst.ID())
	st.SetAuthCacheEntry(sshAuthID, sshReq)

	code, out := doReq(http.MethodGet, "/web/api/auth/"+sshAuthID.String(), nil)
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, true, out["pending"])
	assert.Equal(t, false, out["registration"])
	assert.Equal(t, true, out["sshCheck"])

	// Approve: the verdict must reach the waiting client.
	code, out = doReq(http.MethodPost, "/web/api/auth/"+sshAuthID.String()+"/approve", map[string]any{})
	require.Equal(t, http.StatusOK, code, out)
	assert.NotContains(t, out["status"], "stub")
	assert.Equal(t, true, out["approved"])

	verdict := verdictOf(sshReq)
	assert.True(t, verdict.Accept(), "approve must deliver an accepting verdict")

	// Reject: a failing verdict carrying the rejection error.
	rejAuthID := types.MustAuthID()
	rejReq := types.NewSSHCheckAuthRequest(src.ID(), dst.ID())
	st.SetAuthCacheEntry(rejAuthID, rejReq)

	code, out = doReq(http.MethodPost, "/web/api/auth/"+rejAuthID.String()+"/reject", map[string]any{})
	require.Equal(t, http.StatusOK, code, out)
	assert.NotContains(t, out["status"], "stub")
	assert.Equal(t, false, out["approved"])

	verdict = verdictOf(rejReq)
	assert.False(t, verdict.Accept(), "reject must deliver a failing verdict")
	require.Error(t, verdict.Err)
	assert.Contains(t, verdict.Err.Error(), "rejected")

	// Registration sessions are refused: they are completed
	// via the node's auth URL, and a verdict here (carrying
	// no node) would silently restart the registration.
	regAuthID := types.MustAuthID()
	regReq := types.NewRegisterAuthRequest(&types.RegistrationData{}) //nolint:exhaustruct_v5 // test fixture: only IsRegistration() is consulted
	st.SetAuthCacheEntry(regAuthID, regReq)

	code, _ = doReq(http.MethodPost, "/web/api/auth/"+regAuthID.String()+"/approve", map[string]any{})
	assert.Equal(t, http.StatusConflict, code)

	select {
	case v := <-regReq.WaitForAuth():
		t.Fatalf("approve must not decide a registration session, got verdict %+v", v)
	default:
	}

	// Unknown auth id -> 404.
	code, _ = doReq(http.MethodGet, "/web/api/auth/"+types.MustAuthID().String(), nil)
	assert.Equal(t, http.StatusNotFound, code)

	// Malformed auth id -> 400.
	code, _ = doReq(http.MethodGet, "/web/api/auth/not-an-auth-id", nil)
	assert.Equal(t, http.StatusBadRequest, code)

	// Wrong method -> 405.
	code, _ = doReq(http.MethodPut, "/web/api/auth/"+sshAuthID.String(), map[string]any{})
	assert.Equal(t, http.StatusMethodNotAllowed, code)
}

// TestWebUI_DeleteNodeDelegatesToRealState pins the WebUI
// node-delete endpoint against a real *state.State: the
// DeleteNode delegation interface must be satisfied (no
// stub fallback) and the node must actually disappear from
// the live state. The mock-only coverage
// (webui_write_test.go TestWebUI_DeleteNode) cannot catch a
// signature drift in state.State.DeleteNode: the handler
// type-asserts two DeleteNode shapes and silently answers
// "deleted" from the stub when neither matches, while the
// node stays registered.
//
// Note: this pins the delegation surface only. Propagation
// of the returned change to connected clients (the
// App.Change -> batcher step the API v1/v2 paths perform)
// is tracked separately — see the WebUI write-propagation
// issue in issues/open/.
func TestWebUI_DeleteNodeDelegatesToRealState(t *testing.T) {
	srv := NewServer(t)
	defer srv.Close()

	st := srv.State()

	user := srv.CreateUser(t, "webui-del")

	node := srv.CreateRegisteredNode(t, user, "webui-del-node")

	h := webui.Handler(&types.Config{}, st) //nolint:exhaustruct_v5 // test fixture: zero config

	ts := httptest.NewServer(h)
	defer ts.Close()

	doDelete := func(path string) (int, map[string]any) {
		req, err := http.NewRequestWithContext(
			context.Background(), http.MethodDelete, ts.URL+path, nil)
		require.NoError(t, err)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)

		defer resp.Body.Close()

		out := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&out)

		return resp.StatusCode, out
	}

	nodePath := "/web/api/nodes/" + node.ID().String()

	// DELETE removes the node from the live state.
	code, out := doDelete(nodePath)
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, "deleted", out["status"])
	assert.Equal(t, node.ID().String(), out["nodeID"])

	_, ok := st.GetNodeByID(node.ID())
	require.False(t, ok, "deleted node must be gone from the live state")

	// Deleting the same id again -> 404: the removal is real,
	// not just masked by the response.
	code, _ = doDelete(nodePath)
	assert.Equal(t, http.StatusNotFound, code)

	// Unknown node id -> 404.
	code, _ = doDelete("/web/api/nodes/999999")
	assert.Equal(t, http.StatusNotFound, code)

	// Missing node id -> 400.
	code, _ = doDelete("/web/api/nodes/")
	assert.Equal(t, http.StatusBadRequest, code)

	// Invalid node id -> 400.
	code, _ = doDelete("/web/api/nodes/abc")
	assert.Equal(t, http.StatusBadRequest, code)
}

// TestWebUI_CreateUserDelegatesToRealState pins the WebUI
// user-creation endpoint (POST /web/api/users) against the
// harness's real *state.State. The handler type-asserts
// CreateUser(types.User) (*types.User, change.Change, error)
// and silently answers 201 with a synthesized user when the
// assertion fails — a signature drift in state.State.CreateUser
// would report success while the user is never persisted (the
// same silent-failure class as the delete-node and auth-finish
// delegation tests in this file). Asserts: the created user is
// persisted (GetUserByName), echoed in the response and listed
// by GET /web/api/users; a duplicate name is rejected by the
// users.name unique index without persisting a second user
// (a distinct failure mode: the handler must surface the error,
// not answer 201 for a user that does not exist); missing name
// and malformed JSON -> 400.
func TestWebUI_CreateUserDelegatesToRealState(t *testing.T) {
	srv := NewServer(t)
	defer srv.Close()

	st := srv.State()

	h := webui.Handler(&types.Config{}, st) //nolint:exhaustruct_v5 // test fixture: zero config

	ts := httptest.NewServer(h)
	defer ts.Close()

	doCreate := func(body any) (int, map[string]any) {
		b, err := json.Marshal(body)
		require.NoError(t, err)

		req, err := http.NewRequestWithContext(
			context.Background(), http.MethodPost,
			ts.URL+"/web/api/users", bytes.NewReader(b))
		require.NoError(t, err)

		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)

		defer resp.Body.Close()

		out := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&out)

		return resp.StatusCode, out
	}

	code, out := doCreate(map[string]string{
		"name": "webui-create-user", "email": "webui-create@example.com",
	})
	require.Equal(t, http.StatusCreated, code, out)

	user, ok := out["user"].(map[string]any)
	require.True(t, ok, "response must carry the created user, got %v", out)
	assert.Equal(t, "webui-create-user", user["Name"])

	// The user is persisted in the live state — the stub
	// fallback synthesizes a response without saving.
	created, err := st.GetUserByName("webui-create-user")
	require.NoError(t, err, "created user must be persisted in the live state")
	assert.Equal(t, "webui-create@example.com", created.Email)

	// And it is listed by the users endpoint.
	listReq, err := http.NewRequestWithContext(
		context.Background(), http.MethodGet, ts.URL+"/web/api/users", nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(listReq)
	require.NoError(t, err)

	defer resp.Body.Close()

	var list struct {
		Users []map[string]any `json:"users"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&list))

	found := false

	for _, u := range list.Users {
		if u["name"] == "webui-create-user" {
			found = true
		}
	}

	assert.True(t, found, "created user must be listed by GET /web/api/users")

	// Duplicate name: the partial unique index on
	// users(name) WHERE provider_identifier IS NULL rejects
	// it — the handler must surface the failure (500), not
	// answer 201 for a user that was not created.
	code, _ = doCreate(map[string]string{
		"name": "webui-create-user", "email": "other@example.com",
	})
	assert.Equal(t, http.StatusInternalServerError, code)

	all, err := st.ListAllUsers()
	require.NoError(t, err)

	count := 0

	for _, u := range all {
		if u.Name == "webui-create-user" {
			count++
		}
	}

	assert.Equal(t, 1, count, "duplicate creation must not persist a second user")

	// Missing name -> 400.
	code, _ = doCreate(map[string]string{"email": "noname@example.com"})
	assert.Equal(t, http.StatusBadRequest, code)

	// Malformed JSON -> 400.
	malformed, err := http.NewRequestWithContext(
		context.Background(), http.MethodPost,
		ts.URL+"/web/api/users", bytes.NewReader([]byte("{invalid")))
	require.NoError(t, err)

	malformed.Header.Set("Content-Type", "application/json")

	mresp, err := http.DefaultClient.Do(malformed)
	require.NoError(t, err)

	defer mresp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, mresp.StatusCode)
}

// TestWebUI_ExpiredAPIKeyRejected pins the
// auth-expiry failure mode on the hardened Web UI
// surface. Every other WebUI servertest mounts the
// handler with a zero config, which leaves auth
// disabled entirely (webui.Handler only validates
// credentials under xray.stealth.enforce +
// enforce_control), so a regression that dropped the
// expiry check from the WebUI middleware path would
// stay green. Under the hardened config — the
// production default — a key that expired by the
// clock or via the admin expire flow must be rejected
// with 401, the same verdict the v1 API middleware
// enforces for its surface
// (TestAPIV1AuthMiddlewareExpiredKey). Both
// credential forms (Authorization: Bearer and
// X-API-Key) must refuse the expired key, because
// the middleware accepts either.
func TestWebUI_ExpiredAPIKeyRejected(t *testing.T) {
	srv := NewServer(t)
	defer srv.Close()

	st := srv.State()

	//nolint:exhaustruct_v5 // test fixture: only the auth-gating fields
	cfg := &types.Config{XRay: types.XRayConfig{
		Enabled: true,
		Stealth: types.StealthConfig{Enforce: true, EnforceControl: true}, //nolint:exhaustruct_v5 // test fixture: only the auth-gating fields
	}}

	ts := httptest.NewServer(webui.Handler(cfg, st))
	defer ts.Close()

	status := func(header, value string) int {
		req, err := http.NewRequestWithContext(
			context.Background(), http.MethodGet, ts.URL+"/web/api/nodes", nil)
		require.NoError(t, err)

		if header != "" {
			req.Header.Set(header, value)
		}

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)

		defer resp.Body.Close()

		return resp.StatusCode
	}

	// No credentials and garbage credentials: 401.
	assert.Equal(t, http.StatusUnauthorized, status("", ""))
	assert.Equal(t, http.StatusUnauthorized, status("Authorization", "Bearer garbage"))

	// Live key: both credential forms answer 200.
	liveKey, liveKeyRec, err := st.CreateAPIKey(nil)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, status("Authorization", "Bearer "+liveKey))
	assert.Equal(t, http.StatusOK, status("X-API-Key", liveKey))

	// Key expired by the admin expire flow: 401 on
	// both forms — the operator revoked it from the
	// Web UI/CLI, so it must stop authenticating
	// immediately.
	require.NoError(t, st.ExpireAPIKey(liveKeyRec))

	assert.Equal(t, http.StatusUnauthorized, status("Authorization", "Bearer "+liveKey))
	assert.Equal(t, http.StatusUnauthorized, status("X-API-Key", liveKey))

	// Key expired by the clock (created with a past
	// expiry): 401 as well.
	past := time.Now().Add(-time.Hour)

	clockExpired, _, err := st.CreateAPIKey(&past)
	require.NoError(t, err)

	assert.Equal(t, http.StatusUnauthorized, status("Authorization", "Bearer "+clockExpired))
}
