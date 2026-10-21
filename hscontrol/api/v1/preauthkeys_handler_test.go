package apiv1

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tomiwebpro/stealthscale/hscontrol/state"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
	"github.com/tomiwebpro/stealthscale/hscontrol/types/change"
)

// newPreAuthKeyTestServer builds the v1 API over a real State (sqlite) and
// serves it locally-trusted, mirroring the unix-socket trust boundary. This is
// the fastest harness that exercises the full request path — routing, body
// coercion, middleware shape and the handler/state/DB stack together —
// without Docker. The State is returned so tests can assert on the persisted
// record (e.g. pre-auth key expiry semantics) behind a response.
func newPreAuthKeyTestServer(t *testing.T) (*httptest.Server, *state.State) {
	t.Helper()

	prefixV4 := netip.MustParsePrefix("100.64.0.0/10")
	prefixV6 := netip.MustParsePrefix("fd7a:115c:a1e0::/48")

	cfg := &types.Config{ //nolint:exhaustruct_v5 // test fixture: only db + policy mode matter
		Database: types.DatabaseConfig{ //nolint:exhaustruct_v5 // sqlite-only fixture
			Type: types.DatabaseSqlite,
			Sqlite: types.SqliteConfig{ //nolint:exhaustruct_v5 // path-only fixture
				Path: t.TempDir() + "/preauthkey-v1.db",
			},
		},
		PrefixV4:     &prefixV4,
		PrefixV6:     &prefixV6,
		IPAllocation: types.IPAllocationStrategySequential,
		BaseDomain:   "stealthscale.test",
		Policy: types.PolicyConfig{ //nolint:exhaustruct_v5 // test fixture: mode only
			Mode: types.PolicyModeDB,
		},
	}

	st, err := state.NewState(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	mux, _ := Handler(Backend{
		State:  st,
		Change: func(_ ...change.Change) {},
		Cfg:    cfg,
	})

	srv := httptest.NewServer(WithLocalTrust(mux))
	t.Cleanup(srv.Close)

	return srv, st
}

// requestJSON issues the HTTP call for the given method and decodes the
// response as a generic object; every v1 endpoint answers JSON.
func requestJSON(
	t *testing.T,
	srv *httptest.Server,
	method string,
	path string,
	body map[string]any,
) (int, map[string]any) {
	t.Helper()

	var payload io.Reader
	if body == nil {
		payload = nil
	} else {
		raw, err := json.Marshal(body)
		require.NoError(t, err)

		payload = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(t.Context(), method, srv.URL+path, payload)
	require.NoError(t, err)

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })

	out := map[string]any{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out), "response %d must be JSON", resp.StatusCode)

	return resp.StatusCode, out
}

func createKey(t *testing.T, srv *httptest.Server, body map[string]any) (int, map[string]any) {
	t.Helper()

	return requestJSON(t, srv, http.MethodPost, "/api/v1/preauthkey", body)
}

func preAuthKeyOf(t *testing.T, body map[string]any) map[string]any {
	t.Helper()

	inner, ok := body["preAuthKey"].(map[string]any)
	require.Truef(t, ok, "expected preAuthKey object, got %v", body["preAuthKey"])

	return inner
}

func problemStatus(t *testing.T, body map[string]any) int {
	t.Helper()

	status, ok := body["status"].(float64)
	require.Truef(t, ok, "expected problem status field, got %v", body["status"])

	return int(status)
}

// TestPreAuthKeyHandlers lifecycles the key-issuance failure points through
// the v1 HTTP surface: issuance requires an owner (tag or user), the created
// key material comes back exactly once, validate-id/user errors stay 4xx
// (never a raw 500), and repeated revocation of an id the admin CLI got from
// stale list output is a clean 404.
func TestPreAuthKeyHandlers(t *testing.T) {
	srv, _ := newPreAuthKeyTestServer(t)

	okCode, okBody := createKey(t, srv, map[string]any{
		"user":     "0",
		"reusable": true,
		"aclTags":  []string{"tag:web"},
	})

	t.Run("tagged key with user 0 issues usable key", func(t *testing.T) {
		// Mirrors the CLI default: preauthkeys create --tags sends user "0".
		require.Equal(t, http.StatusOK, okCode, "body: %v", okBody)

		key := preAuthKeyOf(t, okBody)
		require.NotEmpty(t, key["key"], "creation must return the plaintext key")
		require.Equal(t, false, key["used"], "fresh key is unused")
		require.Nil(t, key["user"], "userless key must emit user null")

		tags, ok := key["aclTags"].([]any)
		require.Truef(t, ok, "aclTags must be a list, got %v", key["aclTags"])
		require.Equal(t, []any{"tag:web"}, tags)
	})

	t.Run("unknown user is a 404", func(t *testing.T) {
		code, body := createKey(t, srv, map[string]any{"user": "9999"})
		require.Equal(t, http.StatusNotFound, code, "body: %v", body)
	})

	t.Run("neither user nor tag is a 400", func(t *testing.T) {
		code, body := createKey(t, srv, map[string]any{"user": "0"})
		require.Equal(t, http.StatusBadRequest, code, "body: %v", body)
		require.Equal(t, http.StatusBadRequest, problemStatus(t, body))
	})

	t.Run("tag without tag: prefix is a 400", func(t *testing.T) {
		code, body := createKey(t, srv, map[string]any{"user": "0", "aclTags": []string{"web"}})
		require.Equal(t, http.StatusBadRequest, code, "body: %v", body)
	})

	t.Run("issue body with non-numeric user stays 4xx", func(t *testing.T) {
		code, body := createKey(t, srv, map[string]any{"user": "nope"})
		require.Truef(
			t,
			code >= 400 && code < 500,
			"bad user id must not surface as a server error, got %d: %v",
			code,
			body,
		)
	})

	t.Run("expire lifecycle", func(t *testing.T) {
		code, body := createKey(t, srv, map[string]any{
			"user":       "0",
			"expiration": time.Now().Add(time.Hour).Format(time.RFC3339),
			"aclTags":    []string{"tag:database"},
		})
		require.Equal(t, http.StatusOK, code, "body: %v", body)

		idAny, ok := preAuthKeyOf(t, body)["id"]
		require.Truef(t, ok, "created key must carry an id, got %v", body)
		id, ok := idAny.(string)
		require.Truef(t, ok, "id must be a string, got %v", idAny)

		// Expiring an id that never existed is a clean 404 — the v1 expire
		// endpoint must not implicitly graft ids onto live keys.
		code, body = requestJSON(t, srv, http.MethodPost, "/api/v1/preauthkey/expire", map[string]any{"id": "12345"})
		require.Equal(t, http.StatusNotFound, code, "body: %v", body)

		code, _ = requestJSON(t, srv, http.MethodPost, "/api/v1/preauthkey/expire", map[string]any{"id": id})
		require.Equal(t, http.StatusOK, code, "known id must expire")

		// Delete is idempotent-unsafe by design: a repeat of the same
		// delete (lost response, double click) is a 404, not a 200.
		code, body = requestJSON(t, srv, http.MethodDelete, "/api/v1/preauthkey?id="+id, nil)
		require.Equal(t, http.StatusOK, code, "body: %v", body)

		code, body = requestJSON(t, srv, http.MethodDelete, "/api/v1/preauthkey?id="+id, nil)
		require.Equal(t, http.StatusNotFound, code, "body: %v", body)
	})
}

// TestPreAuthKeyOmittedOrZeroExpirationMintsUsableKey pins the auth-expiry
// failure mode at key *creation*: an omitted (or explicitly zero) expiration
// field must mint a never-expiring key, not an already-dead one. The wire is
// blind to this — a zero-stamped *time.Time serialises the same in list
// output — so the persisted record is resolved through State and asserted
// with PreAuthKey.Validate, the exact check the registration path performs
// when a node tries to use the key.
func TestPreAuthKeyOmittedOrZeroExpirationMintsUsableKey(t *testing.T) {
	srv, st := newPreAuthKeyTestServer(t)

	creates := []struct {
		name string
		body map[string]any
	}{
		{
			name: "omitted expiration",
			body: map[string]any{
				"user":     "0",
				"reusable": true,
				"aclTags":  []string{"tag:web"},
			},
		},
		{
			name: "explicit zero timestamp",
			body: map[string]any{
				"user":       "0",
				"reusable":   true,
				"expiration": "0001-01-01T00:00:00Z",
				"aclTags":    []string{"tag:database"},
			},
		},
	}

	for _, tc := range creates {
		t.Run(tc.name, func(t *testing.T) {
			code, body := createKey(t, srv, tc.body)
			require.Equal(t, http.StatusOK, code, "body: %v", body)

			plain, ok := preAuthKeyOf(t, body)["key"].(string)
			require.Truef(t, ok, "creation must return the plaintext key, got %v", body)

			pak, err := st.GetPreAuthKey(plain)
			require.NoError(t, err)

			require.Nil(t, pak.Expiration, "stored key must not carry an expiration")

			assertNoError := pak.Validate()
			require.NoErrorf(
				t,
				assertNoError,
				"a freshly minted key with no expiration must validate as usable, got %v",
				assertNoError,
			)
		})
	}

	// Contrast case: a key created with a *past* timestamp IS born dead —
	// Validate must reject it. Guards the above against faded assertions.
	code, body := createKey(t, srv, map[string]any{
		"user":       "0",
		"reusable":   true,
		"expiration": time.Now().Add(-time.Hour).Format(time.RFC3339),
		"aclTags":    []string{"tag:gone"},
	})
	require.Equal(t, http.StatusOK, code, "body: %v", body)

	plain, ok := preAuthKeyOf(t, body)["key"].(string)
	require.Truef(t, ok, "creation must return the plaintext key, got %v", body)

	past, err := st.GetPreAuthKey(plain)
	require.NoError(t, err)

	require.Error(t, past.Validate(), "a key minted with a past expiration must be born expired")
}
