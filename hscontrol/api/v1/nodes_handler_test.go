package apiv1

import (
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

type changeRecorder struct {
	count int
}

func (r *changeRecorder) record(cs ...change.Change) {
	r.count += len(cs)
}

// newNodeV1TestServer builds the v1 API over a real State (sqlite) with a
// recording change sink — the same in-process harness the pre-auth-key
// tests use, but nodes writes also need the sink pinned: a write handler
// that drops its change.Change would silently keep every connected client
// on a stale netmap.
func newNodeV1TestServer(t *testing.T) (*httptest.Server, *changeRecorder) {
	t.Helper()

	prefixV4 := netip.MustParsePrefix("100.64.0.0/10")
	prefixV6 := netip.MustParsePrefix("fd7a:115c:a1e0::/48")

	cfg := &types.Config{ //nolint:exhaustruct_v5 // test fixture: only db + policy mode matter
		Database: types.DatabaseConfig{ //nolint:exhaustruct_v5 // sqlite-only fixture
			Type: types.DatabaseSqlite,
			Sqlite: types.SqliteConfig{ //nolint:exhaustruct_v5 // path-only fixture
				Path: t.TempDir() + "/node-v1.db",
			},
		},
		PrefixV4:     &prefixV4,
		PrefixV6:     &prefixV6,
		IPAllocation: types.IPAllocationStrategySequential,
		BaseDomain:   "stealthscale.test",
		Policy: types.PolicyConfig{ //nolint:exhaustruct_v5 // test fixture: mode only
			Mode: types.PolicyModeDB,
		},
		// Flush NodeStore writes immediately: the production batch ticker
		// (500ms) would add ~0.5s to every write in this test.
		Tuning: types.Tuning{ //nolint:exhaustruct_v5 // timeout-only fixture
			NodeStoreBatchTimeout: 5 * time.Millisecond,
		},
	}

	st, err := state.NewState(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	rec := &changeRecorder{count: 0} //nolint:exhaustruct_v5 // test fixture: only the sink matters

	mux, _ := Handler(Backend{ //nolint:exhaustruct_v5 // test fixture: local-trust backend
		State:  st,
		Change: rec.record,
		Cfg:    cfg,
	})

	srv := httptest.NewServer(WithLocalTrust(mux))
	t.Cleanup(srv.Close)

	return srv, rec
}

// provisionNode registers a user-owned node through the real HTTP surface:
// the debug endpoint caches a registration for authID, then /node/register
// completes it via the auth path — the same flow the Web provider's
// "register" command exercises.
func provisionNode(t *testing.T, srv *httptest.Server, user string) string {
	t.Helper()

	code, body := requestJSON(t, srv, http.MethodPost, "/api/v1/user", map[string]any{
		"name": user,
	})
	require.Equal(t, http.StatusOK, code, "body: %v", body)

	code, body = requestJSON(t, srv, http.MethodPut, "/api/v1/policy", map[string]any{
		"policy": `{"tagOwners":{"tag:foo":["` + user + `@"]}}`,
	})
	require.Equal(t, http.StatusOK, code, "body: %v", body)

	authID := types.MustAuthID().String()

	code, body = requestJSON(t, srv, http.MethodPost, "/api/v1/debug/node", map[string]any{
		"name": "n1",
		"key":  authID,
		"user": user,
	})
	require.Equal(t, http.StatusOK, code, "body: %v", body)

	code, body = requestJSON(t, srv, http.MethodPost,
		"/api/v1/node/register?user="+user+"&key="+authID, nil)
	require.Equal(t, http.StatusOK, code, "body: %v", body)

	node, ok := body["node"].(map[string]any)
	require.Truef(t, ok, "expected node object, got %v", body["node"])

	nodeID, ok := node["id"].(string)
	require.Truef(t, ok, "expected node id, got %v", node["id"])

	return nodeID
}

// TestNodeLifecycleHandlers drives the v1 HTTP node writes end to end over a
// real State: admin expiry (the node-expiry failure mode), rename validation,
// the tags-as-identity tag gate, approved-route semantics (exit-route family
// expansion, clear-on-empty) and deletion must all land in state and the
// change sink, with unknown ids failing as mapped 404s.
func TestNodeLifecycleHandlers(t *testing.T) {
	srv, rec := newNodeV1TestServer(t)

	id := provisionNode(t, srv, "nodeuser")

	// Unknown ids must surface as mapped 404s through every admin op —
	// the delete-race sentinel (ErrNodeNotInNodeStore) is the one that
	// reaches mapError in expire/rename/tags-less handlers.
	t.Run("unknown node reads and writes are 404", func(t *testing.T) {
		code, _ := requestJSON(t, srv, http.MethodGet, "/api/v1/node/9999", nil)
		require.Equal(t, http.StatusNotFound, code)

		code, _ = requestJSON(t, srv, http.MethodPost, "/api/v1/node/9999/expire", nil)
		require.Equal(t, http.StatusNotFound, code)

		code, _ = requestJSON(t, srv, http.MethodPost, "/api/v1/node/9999/rename/n2", nil)
		require.Equal(t, http.StatusNotFound, code)

		code, _ = requestJSON(t, srv, http.MethodPost,
			"/api/v1/node/9999/approve_routes", map[string]any{"routes": []string{"10.0.0.0/24"}})
		require.Equal(t, http.StatusNotFound, code)
	})

	t.Run("expire lifecycle", func(t *testing.T) {
		base := rec.count
		future := time.Now().Add(time.Hour).Truncate(time.Millisecond)

		// disableExpiry and a custom expiry are mutually exclusive.
		code, body := requestJSON(t, srv, http.MethodPost, "/api/v1/node/"+id+"/expire", map[string]any{
			"expiry":        time.Now().Add(time.Hour).Format(time.RFC3339Nano),
			"disableExpiry": true,
		})
		require.Equal(t, http.StatusBadRequest, code, "body: %v", body)
		require.Equal(t, base, rec.count, "no change may broadcast for a rejected write")

		// An explicit future expiry is honoured, not clamped to now.
		code, body = requestJSON(t, srv, http.MethodPost, "/api/v1/node/"+id+"/expire", map[string]any{
			"expiry": future.Format(time.RFC3339Nano),
		})
		require.Equal(t, http.StatusOK, code, "body: %v", body)

		node, ok := body["node"].(map[string]any)
		require.Truef(t, ok, "expected node object, got %v", body["node"])
		exp, ok := node["expiry"].(string)
		require.Truef(t, ok, "expected expiry timestamp, got %v", node["expiry"])
		require.Equal(t, exp, future.Format(time.RFC3339Nano), "expiry must round-trip verbatim")

		// Absent body expires the node immediately.
		code, body = requestJSON(t, srv, http.MethodPost, "/api/v1/node/"+id+"/expire", nil)
		require.Equal(t, http.StatusOK, code, "body: %v", body)
		exp, ok = body["node"].(map[string]any)["expiry"].(string)
		require.Truef(t, ok, "expected expiry timestamp, got %v", body["node"])

		past, err := time.Parse(time.RFC3339Nano, exp)
		require.NoError(t, err)
		require.True(t, past.Before(time.Now()), "immediate expiry must be in the past: %v", past)

		// disableExpiry clears the expiry (never expires).
		code, body = requestJSON(t, srv, http.MethodPost, "/api/v1/node/"+id+"/expire", map[string]any{
			"disableExpiry": true,
		})
		require.Equal(t, http.StatusOK, code, "body: %v", body)
		node, ok = body["node"].(map[string]any)
		require.Truef(t, ok, "expected node object, got %v", body["node"])
		require.Nil(t, node["expiry"], "disabled expiry must emit null")

		// Each of the three successful expiries above must have reached
		// the change sink — a dropped change silently strands connected
		// clients on the last netmap until they reconnect.
		require.GreaterOrEqual(t, rec.count, base+3)
	})

	t.Run("rename", func(t *testing.T) {
		base := rec.count
		code, body := requestJSON(t, srv, http.MethodPost, "/api/v1/node/"+id+"/rename/n2", nil)
		require.Equal(t, http.StatusOK, code, "body: %v", body)
		node, ok := body["node"].(map[string]any)
		require.Truef(t, ok, "expected node object, got %v", body["node"])
		require.Equal(t, "n2", node["givenName"], "hostname itself never changes")
		require.Equal(t, "n1", node["name"])
		require.Greater(t, rec.count, base, "the rename must broadcast its change")

		// A name with a space is not a DNS label — it must be rejected
		// before renaming, not silently rendered into a broken FQDN.
		code, _ = requestJSON(t, srv, http.MethodPost,
			"/api/v1/node/"+id+"/rename/machine%20room", nil)
		require.Equal(t, http.StatusBadRequest, code, "invalid DNS label must be a 400")
	})

	t.Run("tags gate and tags-as-identity", func(t *testing.T) {
		base := rec.count
		// Handler-level guards before any state write: empty set (tagged
		// nodes must keep at least one tag) and missing tag: prefix.
		code, body := requestJSON(t, srv, http.MethodPost, "/api/v1/node/"+id+"/tags", map[string]any{})
		require.Equal(t, http.StatusBadRequest, code, "body: %v", body)

		code, body = requestJSON(t, srv, http.MethodPost, "/api/v1/node/"+id+"/tags",
			map[string]any{"tags": []string{"web"}})
		require.Equal(t, http.StatusBadRequest, code, "body: %v", body)

		// State-level guard: a tag not owned by the policy is rejected —
		// tags are identity, not free-form labels.
		code, body = requestJSON(t, srv, http.MethodPost, "/api/v1/node/"+id+"/tags",
			map[string]any{"tags": []string{"tag:bar"}})
		require.Equal(t, http.StatusBadRequest, code, "body: %v", body)

		// Policy-owned tag converts the user-owned node to a tagged node:
		// the response drops the user and the list presents TaggedDevices.
		code, body = requestJSON(t, srv, http.MethodPost, "/api/v1/node/"+id+"/tags",
			map[string]any{"tags": []string{"tag:foo"}})
		require.Equal(t, http.StatusOK, code, "body: %v", body)
		tagged, ok := body["node"].(map[string]any)
		require.Truef(t, ok, "expected node object, got %v", body["node"])
		require.Nil(t, tagged["user"], "tagged node must drop user ownership")

		code, body = requestJSON(t, srv, http.MethodGet, "/api/v1/node", nil)
		require.Equal(t, http.StatusOK, code, "body: %v", body)
		nodes, ok := body["nodes"].([]any)
		require.Truef(t, ok, "expected nodes list, got %v", body["nodes"])
		require.Len(t, nodes, 1)
		listed, ok := nodes[0].(map[string]any)
		require.Truef(t, ok, "expected node object, got %v", nodes[0])
		require.Equal(t, []any{"tag:foo"}, listed["tags"], "tags: %v", listed)
		require.NotNil(t, listed["user"], "tagged node presented as TaggedDevices, not nil")

		require.Greater(t, rec.count, base, "the tag conversion must broadcast its change")
	})

	t.Run("approved routes", func(t *testing.T) {
		base := rec.count
		code, body := requestJSON(t, srv, http.MethodPost, "/api/v1/node/"+id+"/approve_routes",
			map[string]any{"routes": []string{"0.0.0.0/0"}})
		require.Equal(t, http.StatusOK, code, "body: %v", body)

		// One exit route implies both families, else the client won't
		// annotate the node as an exit node.
		node, ok := body["node"].(map[string]any)
		require.Truef(t, ok, "expected node object, got %v", body["node"])
		routes, ok := node["approvedRoutes"].([]any)
		require.Truef(t, ok, "expected approvedRoutes list, got %v", node["approvedRoutes"])
		require.ElementsMatch(t, []any{"0.0.0.0/0", "::/0"}, routes)

		// Clear-on-empty matches the CLI's approve-routes -r "" semantics.
		code, body = requestJSON(t, srv, http.MethodPost, "/api/v1/node/"+id+"/approve_routes",
			map[string]any{"routes": []string{}})
		require.Equal(t, http.StatusOK, code, "body: %v", body)
		node, ok = body["node"].(map[string]any)
		require.Truef(t, ok, "expected node object, got %v", body["node"])
		require.Empty(t, node["approvedRoutes"])

		// A malformed prefix is a 400 before any state write.
		code, body = requestJSON(t, srv, http.MethodPost, "/api/v1/node/"+id+"/approve_routes",
			map[string]any{"routes": []string{"not-a-prefix"}})
		require.Equal(t, http.StatusBadRequest, code, "body: %v", body)

		require.Greater(t, rec.count, base, "route approval must broadcast its change")
	})

	t.Run("delete lifecycle", func(t *testing.T) {
		base := rec.count
		code, body := requestJSON(t, srv, http.MethodDelete, "/api/v1/node/"+id, nil)
		require.Equal(t, http.StatusOK, code, "body: %v", body)

		code, _ = requestJSON(t, srv, http.MethodGet, "/api/v1/node/"+id, nil)
		require.Equal(t, http.StatusNotFound, code, "deleted node must vanish from reads")

		// Lost-response retry of the same delete is a 404, not a 200.
		code, body = requestJSON(t, srv, http.MethodDelete, "/api/v1/node/"+id, nil)
		require.Equal(t, http.StatusNotFound, code, "body: %v", body)

		require.Greater(t, rec.count, base, "node deletion must broadcast its change")
	})

	// Every successful admin write above must have reached the change
	// sink — a dropped change silently strands connected clients on the
	// last netmap until they reconnect.
	require.NotZero(t, rec.count, "no change broadcast for any write")
}
