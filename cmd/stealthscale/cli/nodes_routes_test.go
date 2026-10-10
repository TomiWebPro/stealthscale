package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv1 "github.com/tomiwebpro/stealthscale/gen/client/v1"
)

// approveRoutesHarness serves the two endpoints
// `approve-routes --all` touches — GET the node (to read
// its announced routes) and POST the approval — and records
// every approval body the server receives, so tests can
// assert exactly what the CLI would send.
type approveRoutesHarness struct {
	*httptest.Server

	node     clientv1.Node
	approved [][]string
}

func newApproveRoutesHarness(t *testing.T, node clientv1.Node) *approveRoutesHarness {
	t.Helper()

	h := &approveRoutesHarness{node: node}
	h.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/node/1":
			writeJSON(w, http.StatusOK, clientv1.NodeOutputBody{Node: h.node})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/node/1/approve_routes":
			var body clientv1.SetApprovedRoutesRequestBody
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)

				return
			}

			// A nil Routes pointer (JSON null) and an empty
			// list both clear the node's approved routes
			// server-side; record them alike.
			if body.Routes == nil {
				h.approved = append(h.approved, nil)
			} else {
				h.approved = append(h.approved, *body.Routes)
			}

			writeJSON(w, http.StatusOK, clientv1.NodeOutputBody{Node: h.node})
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	t.Cleanup(h.Close)

	return h
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	// The bodies here are plain generated structs, which
	// json.Marshal cannot fail on.
	payload, _ := json.Marshal(body)

	w.WriteHeader(code)

	_, _ = w.Write(payload)
}

func newApproveRoutesTestClient(t *testing.T, serverURL string) *clientv1.ClientWithResponses {
	t.Helper()

	client, err := clientv1.NewClientWithResponses(serverURL)
	require.NoError(t, err)

	return client
}

func newTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().String("output", "", "output format")

	return cmd
}

// TestApproveRoutesAllExpandsAnnouncedRoutes pins the
// one-command approval flow: `stscale nodes approve-routes
// --identifier 1 --all` fetches the node and POSTs exactly
// its announced (Available) routes, so the operator never
// copies prefixes out of `stscale nodes list-routes`.
func TestApproveRoutesAllExpandsAnnouncedRoutes(t *testing.T) {
	h := newApproveRoutesHarness(t, clientv1.Node{
		Id:              "1",
		GivenName:       "myrouter",
		AvailableRoutes: []string{"10.0.0.0/8", "192.168.0.0/24"},
	})

	err := runApproveRoutes(context.Background(), newApproveRoutesTestClient(t, h.URL), newTestCmd(), 1, nil, true)
	require.NoError(t, err)

	require.Len(t, h.approved, 1)
	assert.Equal(t, []string{"10.0.0.0/8", "192.168.0.0/24"}, h.approved[0])
}

// TestApproveRoutesAllNoAnnouncedRoutes is the failure mode
// of --all: a node that announces nothing must fail fast,
// before any POST — never silently clear the node's approved
// routes with an empty list.
func TestApproveRoutesAllNoAnnouncedRoutes(t *testing.T) {
	h := newApproveRoutesHarness(t, clientv1.Node{
		Id:        "1",
		GivenName: "plainnode",
	})

	err := runApproveRoutes(context.Background(), newApproveRoutesTestClient(t, h.URL), newTestCmd(), 1, nil, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "announces no routes")
	assert.Empty(t, h.approved, "no approval request must be sent")
}

// TestApproveRoutesAllWithRoutesRejected pins the ambiguous
// input guard: --all together with an explicit --routes list
// is refused instead of silently dropping one of them.
func TestApproveRoutesAllWithRoutesRejected(t *testing.T) {
	h := newApproveRoutesHarness(t, clientv1.Node{
		Id:              "1",
		GivenName:       "myrouter",
		AvailableRoutes: []string{"10.0.0.0/8"},
	})

	err := runApproveRoutes(context.Background(), newApproveRoutesTestClient(t, h.URL), newTestCmd(), 1, []string{"10.0.0.0/8"}, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--all cannot be combined with --routes")
	assert.Empty(t, h.approved, "no approval request must be sent")
}

// TestApproveRoutesExplicitRoutesUnchanged is the regression
// guard for the default flow: explicit --routes are POSTed
// verbatim (an empty list still clears all approved routes).
func TestApproveRoutesExplicitRoutesUnchanged(t *testing.T) {
	h := newApproveRoutesHarness(t, clientv1.Node{
		Id:              "1",
		GivenName:       "myrouter",
		AvailableRoutes: []string{"10.0.0.0/8"},
	})

	client := newApproveRoutesTestClient(t, h.URL)

	err := runApproveRoutes(context.Background(), client, newTestCmd(), 1, []string{"10.0.0.0/8", "192.168.0.0/24"}, false)
	require.NoError(t, err)

	require.Len(t, h.approved, 1)
	assert.Equal(t, []string{"10.0.0.0/8", "192.168.0.0/24"}, h.approved[0])

	// The flag default (and `--routes ""`) is a non-nil
	// empty slice, which the server turns into "clear all".
	err = runApproveRoutes(context.Background(), client, newTestCmd(), 1, []string{}, false)
	require.NoError(t, err)

	require.Len(t, h.approved, 2)
	assert.Empty(t, h.approved[1], "empty --routes clears all approved routes")
}
