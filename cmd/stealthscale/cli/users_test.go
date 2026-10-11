package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv1 "github.com/tomiwebpro/stealthscale/gen/client/v1"
)

// usersHarness serves GET /api/v1/user — the only
// endpoint resolveSingleUser talks to — and records
// the query parameters of every request, so tests can
// assert both the resolution result and exactly what
// the CLI asked the server for.
type usersHarness struct {
	*httptest.Server

	users []clientv1.User

	queries []url.Values
}

func newUsersHarness(t *testing.T, users ...clientv1.User) *usersHarness {
	t.Helper()

	//nolint:exhaustruct_v5 // test fixture: the handler and query
	// log are wired up below, not at construction
	h := &usersHarness{users: users}
	h.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/user" {
			http.Error(w, "not found", http.StatusNotFound)

			return
		}

		h.queries = append(h.queries, r.URL.Query())

		writeJSON(w, http.StatusOK, clientv1.ListUsersOutputBody{Users: h.users})
	}))
	t.Cleanup(h.Close)

	return h
}

func newUsersTestClient(t *testing.T, serverURL string) *clientv1.ClientWithResponses {
	t.Helper()

	client, err := clientv1.NewClientWithResponses(serverURL)
	require.NoError(t, err)

	return client
}

// newUserFilterCmd builds a command carrying the
// --name/--identifier flags the way the real user
// subcommands register them.
func newUserFilterCmd(t *testing.T, name string, identifier int64) *cobra.Command {
	t.Helper()

	cmd := newTestCmd()
	usernameAndIDFlag(cmd)

	require.NoError(t, cmd.Flags().Set("name", name))
	require.NoError(t, cmd.Flags().Set("identifier", strconv.FormatInt(identifier, 10)))

	return cmd
}

// TestResolveSingleUserByName pins the happy path
// and the request shape: a name query matching
// exactly one user resolves to that user, and the
// CLI asks the server for that name only.
func TestResolveSingleUserByName(t *testing.T) {
	h := newUsersHarness(t,
		//nolint:exhaustruct_v5 // test fixture: only Id and Name matter
		clientv1.User{Id: "42", Name: "alice"},
	)

	cmd := newUserFilterCmd(t, "alice", -1)

	id, user, err := resolveSingleUser(context.Background(), newUsersTestClient(t, h.URL), cmd)
	require.NoError(t, err)

	require.NotNil(t, user)
	assert.Equal(t, "alice", user.Name)
	assert.Equal(t, uint64(0), id, "a name-only query carries no raw flag id")

	require.Len(t, h.queries, 1)
	assert.Equal(t, "alice", h.queries[0].Get("name"))
	assert.Empty(t, h.queries[0].Get("id"), "a negative identifier must not be sent as a query filter")
}

// TestResolveSingleUserByIdentifier pins the
// identifier direction: the raw flag id is returned
// alongside the matched user and sent as the id
// query filter.
func TestResolveSingleUserByIdentifier(t *testing.T) {
	h := newUsersHarness(t,
		//nolint:exhaustruct_v5 // test fixture: only Id and Name matter
		clientv1.User{Id: "42", Name: "alice"},
	)

	cmd := newUserFilterCmd(t, "", 42)

	id, user, err := resolveSingleUser(context.Background(), newUsersTestClient(t, h.URL), cmd)
	require.NoError(t, err)

	require.NotNil(t, user)
	assert.Equal(t, "alice", user.Name)
	assert.Equal(t, uint64(42), id, "the raw flag id is returned alongside the matched user")

	require.Len(t, h.queries, 1)
	assert.Equal(t, "42", h.queries[0].Get("id"))
	assert.Empty(t, h.queries[0].Get("name"))
}

// TestResolveSingleUserZeroMatches is the failure
// mode this pins: a query that matches nothing must
// report "no users match", not the misleading
// "multiple users match query" the shared error
// used to give for both empty and ambiguous results.
func TestResolveSingleUserZeroMatches(t *testing.T) {
	h := newUsersHarness(t)

	cmd := newUserFilterCmd(t, "nobody", -1)

	_, user, err := resolveSingleUser(context.Background(), newUsersTestClient(t, h.URL), cmd)
	require.Error(t, err)

	assert.Nil(t, user)
	require.ErrorIs(t, err, errNoUsersMatch)
	require.NotErrorIs(t, err, errMultipleUsersMatch)
}

// TestResolveSingleUserMultipleMatches pins the
// ambiguous-query failure mode: more than one match
// is rejected with the specify-an-ID guidance.
func TestResolveSingleUserMultipleMatches(t *testing.T) {
	h := newUsersHarness(t,
		//nolint:exhaustruct_v5 // test fixture: only Id and Name matter
		clientv1.User{Id: "1", Name: "alice"},
		//nolint:exhaustruct_v5 // test fixture: only Id and Name matter
		clientv1.User{Id: "2", Name: "alice"},
	)

	cmd := newUserFilterCmd(t, "alice", -1)

	_, user, err := resolveSingleUser(context.Background(), newUsersTestClient(t, h.URL), cmd)
	require.Error(t, err)

	assert.Nil(t, user)
	require.ErrorIs(t, err, errMultipleUsersMatch)
}

// TestResolveSingleUserRequiresFlags pins the
// missing-input failure mode: neither --name nor
// --identifier means there is nothing to resolve.
func TestResolveSingleUserRequiresFlags(t *testing.T) {
	h := newUsersHarness(t,
		//nolint:exhaustruct_v5 // test fixture: only Id and Name matter
		clientv1.User{Id: "42", Name: "alice"},
	)

	cmd := newUserFilterCmd(t, "", -1)

	_, user, err := resolveSingleUser(context.Background(), newUsersTestClient(t, h.URL), cmd)
	require.Error(t, err)

	assert.Nil(t, user)
	require.ErrorIs(t, err, errFlagRequired)
	assert.Empty(t, h.queries, "no request must be sent without a filter")
}
