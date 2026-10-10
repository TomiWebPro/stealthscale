package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
)

// setUpFlag sets a flag on upCmd and restores its previous
// value when the test ends, so flag state cannot leak
// between tests.
func setUpFlag(t *testing.T, name, value string) {
	t.Helper()

	flag := upCmd.Flags().Lookup(name)
	require.NotNil(t, flag, "up command has no --%s flag", name)

	old := flag.Value.String()
	require.NoError(t, flag.Value.Set(value))

	t.Cleanup(func() {
		require.NoError(t, flag.Value.Set(old))
	})
}

// vlessEndpointFixture serves GET /web/api/vless/{id} like
// the coordinator's Web UI API (hscontrol/webui/webui.go
// handleVLESS). When auth is non-nil the presented
// Authorization header is captured into it. A non-200
// status makes the fixture answer with that status instead
// of the JSON body.
func vlessEndpointFixture(t *testing.T, uri string, status int, auth *string) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth != nil {
			*auth = r.Header.Get("Authorization")
		}

		if status != http.StatusOK {
			http.Error(w, "authentication required", status)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"nodeID":  "1",
			"uuid":    "9f4d4f6c-d1e2-4a3b-9c8d-7a6b5c4d3e2f",
			"port":    10042,
			"uri":     uri,
			"config":  map[string]any{},
			"address": "10.0.0.5",
		})
	}))
	t.Cleanup(srv.Close)

	return srv
}

// TestFetchVLESSURIFetchesEndpointFromWebUIAPI pins the
// discovery contract behind `stscale up --node-id`: the URI
// comes from the coordinator's GET /web/api/vless/{id}, the
// node ID is rendered into the path, and the API key (when
// provided) is sent as a Bearer token.
func TestFetchVLESSURIFetchesEndpointFromWebUIAPI(t *testing.T) {
	const uri = "vless://9f4d4f6c-d1e2-4a3b-9c8d-7a6b5c4d3e2f@10.0.0.5:10042?security=reality_xtls&fp=chrome&dest=www.cloudflare.com%3A443&pbk=pubkey&sid=shortid"

	var gotPath, gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"nodeID":  "1",
			"uuid":    "9f4d4f6c-d1e2-4a3b-9c8d-7a6b5c4d3e2f",
			"port":    10042,
			"uri":     uri,
			"config":  map[string]any{},
			"address": "10.0.0.5",
		})
	}))
	t.Cleanup(srv.Close)

	got, err := fetchVLESSURI(context.Background(), srv.URL, types.NodeID(1), "test-api-key", false)
	require.NoError(t, err)
	assert.Equal(t, uri, got)
	assert.Equal(t, "/web/api/vless/1", gotPath)
	assert.Equal(t, "Bearer test-api-key", gotAuth)
}

// TestFetchVLESSURINoAPIKeySendsNoAuthHeader verifies the
// fetch stays anonymous when no API key is configured (open
// Web UI API, stealth enforcement off) — no empty Bearer
// header is sent.
func TestFetchVLESSURINoAPIKeySendsNoAuthHeader(t *testing.T) {
	const uri = "vless://9f4d4f6c-d1e2-4a3b-9c8d-7a6b5c4d3e2f@10.0.0.5:10042?security=reality_xtls"

	var gotAuth string

	srv := vlessEndpointFixture(t, uri, http.StatusOK, &gotAuth)

	got, err := fetchVLESSURI(context.Background(), srv.URL, types.NodeID(1), "", false)
	require.NoError(t, err)
	assert.Equal(t, uri, got)
	assert.Empty(t, gotAuth)
}

// TestFetchVLESSURIUnauthorized pins the failure mode when
// the coordinator enforces stealth control
// (xray.stealth.enforce_control): the Web UI API answers
// 401 and the CLI must tell the operator how to satisfy it
// (--api-key / STEALTHSCALE_CLI_API_KEY), not just
// surface the status code.
func TestFetchVLESSURIUnauthorized(t *testing.T) {
	srv := vlessEndpointFixture(t, "", http.StatusUnauthorized, nil)

	_, err := fetchVLESSURI(context.Background(), srv.URL, types.NodeID(1), "", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 401")
	assert.Contains(t, err.Error(), "--api-key")
	assert.Contains(t, err.Error(), "STEALTHSCALE_CLI_API_KEY")
}

// TestFetchVLESSURIMissingURI verifies a 200 response
// without a URI is an error, not a silently empty endpoint.
func TestFetchVLESSURIMissingURI(t *testing.T) {
	srv := vlessEndpointFixture(t, "", http.StatusOK, nil)

	_, err := fetchVLESSURI(context.Background(), srv.URL, types.NodeID(1), "", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no VLESS URI")
}

// TestUpNodeIDJoinsWithoutCopyPaste pins the one-command
// join: with --node-id and --coordinator, `stscale up`
// fetches the endpoint from the coordinator and dials it —
// no --vless-uri copy/paste. The flow is expected to fail
// at the VLESS dial (the fixture server is not a VLESS
// listener); the error must prove the fetched URI was used,
// not the missing-flag error.
func TestUpNodeIDJoinsWithoutCopyPaste(t *testing.T) {
	// The handler reads uri at request time, so the fixture
	// can serve a URI that points at the fixture server
	// itself — the dial then proves the fetched URI was used.
	var uri string

	var gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"nodeID":  "1",
			"uuid":    "9f4d4f6c-d1e2-4a3b-9c8d-7a6b5c4d3e2f",
			"port":    10042,
			"uri":     uri,
			"config":  map[string]any{},
			"address": "10.0.0.5",
		})
	}))
	t.Cleanup(srv.Close)

	// reality_xtls without pbk fails fast inside DialVLESS
	// (fail-closed: refuses to downgrade to plain uTLS).
	uri = fmt.Sprintf(
		"vless://9f4d4f6c-d1e2-4a3b-9c8d-7a6b5c4d3e2f@%s?security=reality_xtls&fp=chrome&dest=www.cloudflare.com%%3A443",
		srv.Listener.Addr().String(),
	)

	setUpFlag(t, "coordinator", srv.URL)
	setUpFlag(t, "node-id", "1")
	setUpFlag(t, "authkey", "test-auth-key")
	setUpFlag(t, "api-key", "test-api-key")
	setUpFlag(t, "timeout", "5s")

	err := upCmd.RunE(upCmd, []string{})
	require.Error(t, err)

	// URI resolution succeeded — the failure is the stealth
	// transport dial against the plain-HTTP fixture server.
	assert.Contains(t, err.Error(), "VLESS dial failed")
	assert.NotContains(
		t,
		err.Error(),
		"--vless-uri (or --endpoint) is required",
		"the endpoint must come from --node-id, not the missing-flag error",
	)
	assert.Contains(
		t,
		err.Error(),
		srv.Listener.Addr().String(),
		"the dial must target the address from the fetched URI",
	)
	assert.Equal(t, "Bearer test-api-key", gotAuth)
}

// TestUpNodeIDRequiresCoordinator pins the guard: --node-id
// without --coordinator is a usage error (the endpoint has
// to be fetched from somewhere).
func TestUpNodeIDRequiresCoordinator(t *testing.T) {
	setUpFlag(t, "node-id", "1")

	err := upCmd.RunE(upCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--node-id requires --coordinator")
}

// TestUpAddressOverrideAppliesToFetchedEndpoint pins the
// override for the common case where the coordinator's
// xray.listen_addr is 0.0.0.0 (not dialable from a client):
// --address replaces the address in the fetched URI before
// dialing, so the join stays copy/paste-free.
func TestUpAddressOverrideAppliesToFetchedEndpoint(t *testing.T) {
	// The fixture advertises a non-dialable address; the
	// override replaces it before the dial.
	srv := vlessEndpointFixture(
		t,
		"vless://9f4d4f6c-d1e2-4a3b-9c8d-7a6b5c4d3e2f@10.0.0.5:10042?security=reality_xtls&fp=chrome&dest=www.cloudflare.com%3A443",
		http.StatusOK,
		nil,
	)

	setUpFlag(t, "coordinator", srv.URL)
	setUpFlag(t, "node-id", "1")
	setUpFlag(t, "authkey", "test-auth-key")
	setUpFlag(t, "address", "127.0.0.1")
	setUpFlag(t, "timeout", "5s")

	err := upCmd.RunE(upCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "VLESS dial failed")
	assert.Contains(t, err.Error(), "127.0.0.1:10042")
	assert.NotContains(t, err.Error(), "10.0.0.5")

	// The dial-failure hints must point at real CLI surface:
	// 'stscale nodes vless' prints the PBK/SID/Dest/FP columns
	// by default. There is no --verbose flag — a hint that
	// references a non-existent flag is a dead end for the user.
	assert.Contains(t, err.Error(), "stscale nodes vless")
	assert.NotContains(t, err.Error(), "--verbose")
}
