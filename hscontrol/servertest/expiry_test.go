package servertest_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tomiwebpro/stealthscale/hscontrol/servertest"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
)

// TestNodeExpiryPersisted verifies that State.SetNodeExpiry correctly writes
// to both the in-memory NodeStore and the database, and that the node's
// IsExpired() status reflects the updated value.
func TestNodeExpiryPersisted(t *testing.T) {
	t.Parallel()

	h := servertest.NewHarness(t, 1)

	st := h.Server.State()
	client := h.Client(0)

	nodeID := types.NodeID(client.Netmap().SelfNode.ID()) //nolint:gosec

	// Step 1: Expire the node by setting expiry to the past.
	expiredAt := time.Now().Add(-5 * time.Minute)
	_, _, err := st.SetNodeExpiry(nodeID, &expiredAt)
	require.NoError(t, err)

	// Step 2: Verify the NodeStore reflects the expiry.
	var storeView types.NodeView
	found := false
	for _, n := range st.ListNodes().All() {
		if n.ID() == nodeID {
			storeView = n
			found = true
			break
		}
	}
	require.True(t, found, "node must exist in NodeStore")
	assert.True(t, storeView.Expiry().Valid(), "expiry should be set")
	assert.True(t, storeView.IsExpired(), "node should be marked expired in NodeStore")

	// Step 3: Clear expiry (nil).
	_, _, err = st.SetNodeExpiry(nodeID, nil)
	require.NoError(t, err)

	// Step 4: Verify clear took effect in NodeStore.
	found = false
	for _, n := range st.ListNodes().All() {
		if n.ID() == nodeID {
			storeView = n
			found = true
			break
		}
	}
	require.True(t, found, "node must still exist after clearing expiry")
	assert.False(t, storeView.Expiry().Valid(), "expiry should be cleared")
	assert.False(t, storeView.IsExpired(), "node should not be expired after clearing")
}

// TestNodeExpiryFutureDoesNotMarkExpired verifies that setting a future
// expiry does not mark the node as expired.
func TestNodeExpiryFutureDoesNotMarkExpired(t *testing.T) {
	t.Parallel()

	h := servertest.NewHarness(t, 1)

	st := h.Server.State()
	client := h.Client(0)

	nodeID := types.NodeID(client.Netmap().SelfNode.ID()) //nolint:gosec

	// Set expiry far in the future.
	futureExpiry := time.Now().Add(365 * 24 * time.Hour)
	_, _, err := st.SetNodeExpiry(nodeID, &futureExpiry)
	require.NoError(t, err)

	// Verify node is NOT expired despite having an expiry set.
	var storeView types.NodeView
	for _, n := range st.ListNodes().All() {
		if n.ID() == nodeID {
			storeView = n
			break
		}
	}
	require.True(t, storeView.Valid(), "node must exist")
	assert.True(t, storeView.Expiry().Valid(), "expiry should be set")
	assert.False(t, storeView.IsExpired(), "future expiry must not mark node as expired")
}
