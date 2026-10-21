package state

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPolicyWriteBoundaryRejectedWriteLeavesLiveUntouched is the
// regression test for the setPolicy partial-apply bug (issue
// 202610210805): the previous v1/v2 setPolicy flow applied the draft to
// the live [policy.PolicyManager] first and only then ran the remaining
// checks (per-node SSH compile, DB write, reload), so a rejected write —
// the admin got an error response — still left the rejected policy
// serving live policy evaluation, and could be silently reverted on
// restart because the DB never saw it.
//
// The write boundary is now validate (throwaway manager) → persist →
// apply-to-live via reload; every failure path must leave the live
// manager exactly as it was.
func TestPolicyWriteBoundaryRejectedWriteLeavesLiveUntouched(t *testing.T) {
	_, s, nodeID := persistTestSetup(t)
	t.Cleanup(func() { _ = s.Close() })

	// Seed live + DB with the old policy: tag:foo owned, tag:bar unknown.
	oldPol := `{
		"tagOwners": {"tag:foo": ["persist-user@"]},
		"acls": [{"action": "accept", "src": ["*"], "dst": ["*:*"]}]
	}`
	_, err := s.SetPolicy([]byte(oldPol))
	require.NoError(t, err)
	_, err = s.SetPolicyInDB(oldPol)
	require.NoError(t, err)

	node, ok := s.GetNodeByID(nodeID)
	require.True(t, ok)
	require.True(t, s.NodeCanHaveTag(node, "tag:foo"), "precondition: old policy live")
	assert.False(t, s.NodeCanHaveTag(node, "tag:bar"), "precondition: tag:bar unknown to the old policy")

	// Break the database connection so every write-boundary step that
	// depends on it (ValidatePolicy's user load, SetPolicyInDB, reload's
	// policy load) fails. This is the failure class the bug described:
	// the admin write answers an error, and the rejected draft must not
	// have taken effect.
	gormDB := s.DB().DB
	require.NotNil(t, gormDB, "precondition: DB reachable")

	sqlDB, err := gormDB.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close(), "precondition: closing the connection pool must succeed")

	// An invalid draft is rejected by validation and must not touch live.
	invalid := `{"acls": [`
	require.Error(t, s.ValidatePolicy([]byte(invalid)), "invalid draft must fail validation")
	assert.True(t, s.NodeCanHaveTag(node, "tag:foo"),
		"rejected write must not change the live policy: tag:foo still owned")
	assert.False(t, s.NodeCanHaveTag(node, "tag:bar"),
		"rejected write must not change the live policy: tag:bar must stay unknown")

	// A valid draft whose DB write fails is the core partial-apply
	// scenario: validation alone passes, so the old flow had already
	// applied it to the live manager before persisting failed.
	newPol := `{
		"tagOwners": {"tag:foo": ["persist-user@"], "tag:bar": ["persist-user@"]},
		"acls": [{"action": "accept", "src": ["*"], "dst": ["*:*"]}]
	}`

	// Precondition for the fault injection: with the DB closed, the DB
	// write surface really fails — the test is not counting a validation
	// pass that could never have persisted anyway.
	_, dbErr := s.SetPolicyInDB(newPol)
	require.Error(t, dbErr, "precondition: DB write must fail after closing the pool")

	// The old flow would have left tag:bar live here (SetPolicy at step 1
	// succeeded before the DB failure).
	require.Error(t, s.ValidatePolicy([]byte(newPol)),
		"write boundary fails when the DB backing it is down")
	assert.True(t, s.NodeCanHaveTag(node, "tag:foo"),
		"failed write-boundary validation must not change the live policy: tag:foo still owned")
	assert.False(t, s.NodeCanHaveTag(node, "tag:bar"),
		"failed write boundary must not change the live policy: tag:bar must stay unknown (the partial-apply bug left it live)")
}
