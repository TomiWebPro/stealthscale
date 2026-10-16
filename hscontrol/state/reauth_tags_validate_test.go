package state

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tomiwebpro/stealthscale/hscontrol/db"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
)

// TestValidateRequestTagsForReauth pins the re-auth tag gate
// (validateRequestTagsForReauth): empty requests pass, tags the node or the
// authenticating user owns pass, and anything else is reported rejected.
// This is a failure-point contract — a re-auth carrying an unowned or
// undefined tag must be rejected, not silently kept or silently dropped.
func TestValidateRequestTagsForReauth(t *testing.T) {
	dbPath := t.TempDir() + "/stealthscale.db"
	cfg := persistTestConfig(dbPath)

	database, err := db.NewStealthScaleDatabase(cfg)
	require.NoError(t, err)

	user := database.CreateUserForTest("reauthtag-user")
	database.CreateRegisteredNodeForTest(user, "reauthtag-node")

	require.NoError(t, database.Close())

	s, err := NewState(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	policy := fmt.Sprintf(`{"tagOwners":{"tag:foo":["%s@"]}}`, user.Name)
	_, err = s.SetPolicy([]byte(policy))
	require.NoError(t, err)

	// Views built directly: no IPs, so the IP-set half of NodeCanHaveTag
	// cannot fire and each authorisation half is exercised in isolation.
	ownedNode := (&types.Node{Hostname: "owned", User: user}).View() //nolint:exhaustruct_v5 // test fixture: only fields under test
	bareNode := (&types.Node{Hostname: "bare"}).View()               //nolint:exhaustruct_v5 // test fixture: only fields under test
	userView := user.View()
	anonView := types.UserView{}

	t.Run("empty request returns nil", func(t *testing.T) {
		require.Nil(t, s.validateRequestTagsForReauth(ownedNode, userView, nil))
		require.Nil(t, s.validateRequestTagsForReauth(bareNode, anonView, []string{}))
	})

	t.Run("node-owned tag accepted", func(t *testing.T) {
		require.Empty(t, s.validateRequestTagsForReauth(ownedNode, userView, []string{"tag:foo"}))
	})

	t.Run("auth-user-owned tag accepted for userless node", func(t *testing.T) {
		require.Empty(t, s.validateRequestTagsForReauth(bareNode, userView, []string{"tag:foo"}))
	})

	t.Run("invalid user cannot rescue tag", func(t *testing.T) {
		require.Equal(t,
			[]string{"tag:foo"},
			s.validateRequestTagsForReauth(bareNode, anonView, []string{"tag:foo"}),
		)
	})

	t.Run("undefined tag rejected", func(t *testing.T) {
		require.Equal(t,
			[]string{"tag:bar"},
			s.validateRequestTagsForReauth(ownedNode, userView, []string{"tag:bar"}),
		)
	})

	t.Run("mixed request rejects only unowned tags", func(t *testing.T) {
		require.Equal(t,
			[]string{"tag:bar"},
			s.validateRequestTagsForReauth(ownedNode, userView, []string{"tag:foo", "tag:bar"}),
		)
	})
}
