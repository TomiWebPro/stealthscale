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
