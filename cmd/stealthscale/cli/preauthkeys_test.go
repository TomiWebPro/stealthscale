package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv1 "github.com/tomiwebpro/stealthscale/gen/client/v1"
)

// preauthKeysHarness serves POST /api/v1/preauthkey —
// the create endpoint — and records the decoded request
// body of every request, so tests can assert exactly
// what the CLI sends on the wire.
type preauthKeysHarness struct {
	*httptest.Server

	bodies []clientv1.CreatePreAuthKeyRequestBody
}

func newPreauthKeysHarness(t *testing.T) *preauthKeysHarness {
	t.Helper()

	//nolint:exhaustruct_v5 // test fixture: fields are wired below
	h := &preauthKeysHarness{}
	h.Server = httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")

			if r.Method != http.MethodPost || r.URL.Path != "/api/v1/preauthkey" {
				http.Error(w, "not found", http.StatusNotFound)

				return
			}

			var body clientv1.CreatePreAuthKeyRequestBody

			decodeErr := json.NewDecoder(r.Body).Decode(&body)
			if decodeErr != nil {
				http.Error(w, decodeErr.Error(), http.StatusBadRequest)

				return
			}

			h.bodies = append(h.bodies, body)

			//nolint:exhaustruct_v5 // test fixture: response shape not under test
			writeJSON(w, http.StatusOK, clientv1.PreAuthKeyOutputBody{
				PreAuthKey: clientv1.PreAuthKey{
					Id:         "7",
					Key:        "hskey-secret-test",
					Expiration: time.Now().Add(time.Hour),
				},
			})
		}),
	)
	t.Cleanup(h.Close)

	return h
}

// resetPreAuthKeyFlags clears the state the shared rootCmd
// keeps between Execute() calls, so later tests don't
// inherit an earlier test's --user value.
func resetPreAuthKeyFlags(t *testing.T) {
	t.Helper()

	require.NoError(t, createPreAuthKeyCmd.Flags().Set("user", "0"))
}

// TestPreAuthKeyCreateRequiresOwner pins the missing-input
// failure mode of `stscale preauthkeys create`: a key owned
// by neither a user nor tags must fail locally — before any
// server roundtrip — instead of shipping user:"0" to the
// coordinator for an awkward remote rejection.
func TestPreAuthKeyCreateRequiresOwner(t *testing.T) {
	h := newPreauthKeysHarness(t)
	setViperCLIConfig(t, h.URL)

	rootCmd.SetArgs([]string{"preauthkeys", "create"})

	err := rootCmd.Execute()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--user or --tags")
	assert.Empty(t, h.bodies, "no request must hit the server without an owner")
}

// TestPreAuthKeyCreateSendsUser pins the documented
// user-owned form (docs/ref/registration.md: `create
// --user <USER_ID>`): the user flag is sent verbatim,
// and only then.
func TestPreAuthKeyCreateSendsUser(t *testing.T) {
	h := newPreauthKeysHarness(t)
	setViperCLIConfig(t, h.URL)

	rootCmd.SetArgs([]string{"preauthkeys", "create", "--user", "42"})
	require.NoError(t, rootCmd.Execute())

	require.Len(t, h.bodies, 1)
	require.NotNil(t, h.bodies[0].User)
	assert.Equal(t, "42", *h.bodies[0].User)
	assert.NotZero(t, h.bodies[0].Expiration, "the 1h default expiry must be sent")
}

// TestPreAuthKeyCreateTagsOnlyOmitsUser pins the documented
// tagged form (docs/ref/registration.md: `create --tags
// tag:<TAG>`): no user field may leak on the wire, not even
// the unset flag's "0" — the coordinator would read user 0
// as an unknown user.
func TestPreAuthKeyCreateTagsOnlyOmitsUser(t *testing.T) {
	resetPreAuthKeyFlags(t)

	h := newPreauthKeysHarness(t)
	setViperCLIConfig(t, h.URL)

	rootCmd.SetArgs([]string{"preauthkeys", "create", "--tags", "tag:server"})
	require.NoError(t, rootCmd.Execute())

	require.Len(t, h.bodies, 1)
	assert.Nil(t, h.bodies[0].User, "a tagged key must not carry a user field")
	require.NotNil(t, h.bodies[0].AclTags)
	assert.Equal(t, []string{"tag:server"}, *h.bodies[0].AclTags)
}
