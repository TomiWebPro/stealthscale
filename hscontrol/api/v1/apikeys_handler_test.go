package apiv1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tomiwebpro/stealthscale/hscontrol/db"
	"github.com/tomiwebpro/stealthscale/hscontrol/state"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
	"github.com/tomiwebpro/stealthscale/hscontrol/types/change"
)

// newApiKeyV1TestServer builds the v1 API over a real State (sqlite), the same
// in-process harness the pre-auth-key tests use. It also returns the State so
// the lifecycle tests can assert the auth-tier behaviour
// (State.AuthenticateAPIKey) the v2 API actually relies on — a key that lists
// fine but cannot authenticate is broken in a way list-only assertions cannot
// see.
func newApiKeyV1TestServer(t *testing.T) (*httptest.Server, *state.State) {
	t.Helper()

	prefixV4 := netip.MustParsePrefix("100.64.0.0/10")
	prefixV6 := netip.MustParsePrefix("fd7a:115c:a1e0::/48")

	cfg := &types.Config{ //nolint:exhaustruct_v5 // test fixture: only db + policy mode matter
		Database: types.DatabaseConfig{ //nolint:exhaustruct_v5 // sqlite-only fixture
			Type: types.DatabaseSqlite,
			Sqlite: types.SqliteConfig{ //nolint:exhaustruct_v5 // path-only fixture
				Path: t.TempDir() + "/apikey-v1.db",
			},
		},
		PrefixV4:     &prefixV4,
		PrefixV6:     &prefixV6,
		IPAllocation: types.IPAllocationStrategySequential,
		BaseDomain:   "stealthscale.test",
		Policy: types.PolicyConfig{ //nolint:exhaustruct_v5 // mode-only fixture
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

func createApiKeyByBody(t *testing.T, srv *httptest.Server, body map[string]any) (int, string) {
	t.Helper()

	code, out := requestJSON(t, srv, http.MethodPost, "/api/v1/apikey", body)

	raw, _ := out["apiKey"].(string)

	return code, raw
}

// newestApiKey lists the keys and returns the entry with the highest id — in
// this isolated fixture the autoincrement seq makes that the most recently
// created key.
func newestApiKey(t *testing.T, srv *httptest.Server) map[string]any {
	t.Helper()

	code, body := requestJSON(t, srv, http.MethodGet, "/api/v1/apikey", nil)
	require.Equal(t, http.StatusOK, code, "body: %v", body)

	keys, ok := body["apiKeys"].([]any)
	require.Truef(t, ok, "expected apiKeys array, got %v", body["apiKeys"])
	require.NotEmpty(t, keys)

	newest := uint64(0)
	out := map[string]any{}

	for _, k := range keys {
		entry, ok := k.(map[string]any)
		require.Truef(t, ok, "expected apiKey object, got %v", k)

		idStr, ok := entry["id"].(string)
		require.Truef(t, ok, "expected string id, got %v", entry["id"])

		id, err := strconv.ParseUint(idStr, 10, 64)
		require.NoError(t, err)

		if id > newest {
			newest = id
			out = entry
		}
	}

	return out
}

// TestApiKeyHandlers lifecycles the api-key failure points through the v1 HTTP
// surface, mirroring `stscale apikeys create/expire/delete` and the Web UI's
// keys tab: the raw key material is returned once and never again, the masked
// list output cannot leak it, an expired key fails closed with a distinct
// sentinel, a deleted key disappears from lookups and rejects late retries
// with a clean 404, and every malformed id/prefix selection stays 4xx.
//
// The no-expiration subtest pins the contract the Web UI handleCreateAPIKey
// already implements (empty expiry → never expires): an empty expiration body
// must mint a working key, not one the validator treats as expired.
func TestApiKeyHandlers(t *testing.T) {
	srv, st := newApiKeyV1TestServer(t)

	t.Run("create returns raw key once and list output never leaks it", func(t *testing.T) {
		expiration := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
		code, raw := createApiKeyByBody(t, srv, map[string]any{"expiration": expiration})
		require.Equal(t, http.StatusOK, code)
		require.NotEmpty(t, raw, "create must return the raw key material")

		entry := newestApiKey(t, srv)

		// The masked prefix is all that may appear in list output.
		prefix, _ := entry["prefix"].(string)
		assert.Regexp(t, `^hskey-api-.{12}-\*\*\*$`, prefix, "masked prefix shape: hskey-api-<12>-***")

		// The secret is everything after the second dash; the list response
		// must not contain it anywhere (the 12-char prefix legitimately
		// appears in both the raw key and the masked output).
		_, rest, found := strings.Cut(raw, "hskey-api-")
		require.True(t, found)

		_, secret, _ := strings.Cut(rest, "-")

		encoded, err := json.Marshal(entry)
		require.NoError(t, err)

		assert.NotContainsf(t, string(encoded), secret,
			"list output must never contain the key secret")
		assert.NotContains(t, string(encoded), raw,
			"list output must never contain the raw key")

		assert.Nil(t, entry["lastSeen"], "a fresh key has never been seen")
		assert.NotNil(t, entry["expiration"], "an explicitly-expiring key round-trips its expiration")

		// The minted key must authenticate on the path the v2 API uses.
		authed, err := st.AuthenticateAPIKey(raw)
		require.NoError(t, err)
		assert.NotNil(t, authed)
	})

	t.Run("empty expiration creates a never-expiring key", func(t *testing.T) {
		// An omitted expiration field is what the generated client/CLI sends
		// when the operator opts out of expiry: an empty JSON object.
		code, raw := createApiKeyByBody(t, srv, map[string]any{})
		require.Equal(t, http.StatusOK, code)
		require.NotEmpty(t, raw)

		entry := newestApiKey(t, srv)

		assert.Nilf(t, entry["expiration"],
			"a key minted without expiration must not carry a zero-time expiration, got %v", entry["expiration"])

		authed, err := st.AuthenticateAPIKey(raw)
		require.NoError(t, err, "the minted key must authenticate, not sit dead on the shelf")
		assert.NotNil(t, authed)
	})

	t.Run("expired key fails closed with the expired sentinel", func(t *testing.T) {
		_, raw := createApiKeyByBody(t, srv, map[string]any{
			"expiration": time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
		})

		entry := newestApiKey(t, srv)
		idStr, _ := entry["id"].(string)

		code, body := requestJSON(t, srv, http.MethodPost, "/api/v1/apikey/expire", map[string]any{"id": idStr})
		require.Equal(t, http.StatusOK, code, "body: %v", body)

		authed, err := st.AuthenticateAPIKey(raw)
		assert.Nil(t, authed)
		require.Error(t, err)
		assert.ErrorIs(t, err, db.ErrAPIKeyExpired)
	})

	t.Run("delete removes key and stale retry is 404", func(t *testing.T) {
		_, raw := createApiKeyByBody(t, srv, map[string]any{})

		entry := newestApiKey(t, srv)
		prefix, _ := entry["prefix"].(string)
		idStr, _ := entry["id"].(string)

		require.NotEmpty(t, prefix)

		code, body := requestJSON(t, srv, http.MethodDelete, "/api/v1/apikey/"+prefix, nil)
		require.Equal(t, http.StatusOK, code, "body: %v", body)

		// The key is gone from lookups...
		_, err := st.AuthenticateAPIKey(raw)
		require.Error(t, err, "a deleted key must not authenticate")

		code, body = requestJSON(t, srv, http.MethodGet, "/api/v1/apikey", nil)
		require.Equal(t, http.StatusOK, code)

		keys, ok := body["apiKeys"].([]any)
		require.Truef(t, ok, "expected apiKeys array, got %v", body["apiKeys"])

		for _, k := range keys {
			other, ok := k.(map[string]any)
			require.True(t, ok)

			assert.NotEqual(t, idStr, other["id"], "deleted key must not appear in the list")
		}

		// ...and a retry with the stale list output is a clean 404, not a 500.
		code, body = requestJSON(t, srv, http.MethodDelete, "/api/v1/apikey/"+prefix, nil)
		assert.Equalf(t, http.StatusNotFound, code, "body: %v", body)
	})

	t.Run("lookup input errors stay 4xx", func(t *testing.T) {
		cases := []struct {
			name string
			body map[string]any
			want int
		}{
			{"expire: neither id nor prefix", nil, http.StatusBadRequest},
			{"expire: both id and prefix", map[string]any{
				"id": "1", "prefix": "hskey-api-abcd1234efgh-***",
			}, http.StatusBadRequest},
			{"expire: unknown id", map[string]any{"id": "999999"}, http.StatusNotFound},
			{"expire: unknown prefix", map[string]any{
				"prefix": "hskey-api-zzzzzzzzzzzz-***",
			}, http.StatusNotFound},
			{"expire: non-numeric id", map[string]any{"id": "not-a-num"}, http.StatusBadRequest},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				code, body := requestJSON(t, srv, http.MethodPost, "/api/v1/apikey/expire", tc.body)
				require.Equal(t, tc.want, code, "body: %v", body)
			})
		}
	})
}
