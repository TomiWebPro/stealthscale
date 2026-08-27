// Copyright (c) 2026 TomiWebPro <TomiWebPro@gmail.com>
// SPDX-License-Identifier: BSD-3-Clause

package webui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
