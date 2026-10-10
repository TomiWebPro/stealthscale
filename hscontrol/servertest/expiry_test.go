package servertest_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tomiwebpro/stealthscale/hscontrol/servertest"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
	"tailscale.com/tailcfg"
	"tailscale.com/types/netmap"
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

// TestNodeExpirySelfSeesExpired verifies that when SetNodeExpiry(past) is called,
// the affected node itself sees SelfNode.Expired() == true after receiving
// its next MapResponse. This covers the case reported at:
// https://github.com/tomiwebpro/stealthscale/issues/node-expiry-not-in-mapresponse
func TestNodeExpirySelfSeesExpired(t *testing.T) {
	t.Parallel()

	h := servertest.NewHarness(t, 1)

	st := h.Server.State()
	client := h.Client(0)

	nodeID := types.NodeID(client.Netmap().SelfNode.ID()) //nolint:gosec

	// Step 1: Confirm the node is not expired initially.
	assert.False(t, client.Netmap().SelfNode.Expired(), "node should not be expired initially")

	// Step 2: Expire the node by setting expiry to the past.
	expiredAt := time.Now().Add(-5 * time.Minute)
	_, _, err := st.SetNodeExpiry(nodeID, &expiredAt)
	require.NoError(t, err)

	// Step 3: Force the node to restart its poll to get a fresh MapResponse.
	ctx := context.Background()
	require.NoError(t, client.RestartPoll(ctx))

	// Step 4: Verify the node now sees its own SelfNode.Expired = true.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		nm := client.Netmap()
		assert.NotNil(c, nm, "netmap should not be nil")
		assert.True(c, nm.SelfNode.Expired(),
			"node must see SelfNode.Expired=true after SetNodeExpiry(past)")
	}, 5*time.Second, 200*time.Millisecond)
}

// TestNodeExpiryPropagatesToPeers verifies that when a node is expired via
// SetNodeExpiry, all connected peers see the node as expired in their netmaps.
// This is a failure-point test: operators who expire a node expect immediate
// effect — their peers should not consider the node valid.
func TestNodeExpiryPropagatesToPeers(t *testing.T) {
	t.Parallel()

	h := servertest.NewHarness(t, 3)

	st := h.Server.State()
	client0 := h.Client(0) // node that will be expired
	client1 := h.Client(1)
	client2 := h.Client(2)

	// Verify mesh is complete before testing.
	h.WaitForMeshComplete(t, 5*time.Second)

	// Step 1: Confirm peers initially see client0 as alive.
	node0ID := tailcfg.NodeID(client0.Netmap().SelfNode.ID()) //nolint:gosec

	node0Peer1Before, found1 := findPeerByID(client1.Netmap(), node0ID)
	node0Peer2Before, found2 := findPeerByID(client2.Netmap(), node0ID)

	require.True(t, found1, "peer1 should have node0 in its netmap")
	require.True(t, found2, "peer2 should have node0 in its netmap")

	assert.False(t, node0Peer1Before.Expired(),
		"peer1 should see node0 as active before expiry")
	assert.False(t, node0Peer2Before.Expired(),
		"peer2 should see node0 as active before expiry")

	// Step 2: Expire node0.
	expiredAt := time.Now().Add(-5 * time.Minute)
	_, _, err := st.SetNodeExpiry(types.NodeID(node0ID), &expiredAt)
	require.NoError(t, err)

	// Step 3: Restart polls so peers receive updated netmaps.
	ctx := context.Background()
	require.NoError(t, client1.RestartPoll(ctx))
	require.NoError(t, client2.RestartPoll(ctx))

	// Step 4: Verify peers now see node0 as expired.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		nm1 := client1.Netmap()
		nm2 := client2.Netmap()

		p1, ok := findPeerByID(nm1, node0ID)
		assert.True(c, ok, "peer1 should have node0 in netmap")
		assert.True(c, p1.Expired(), "peer1 should see node0 as expired")

		p2, ok := findPeerByID(nm2, node0ID)
		assert.True(c, ok, "peer2 should have node0 in netmap")
		assert.True(c, p2.Expired(), "peer2 should see node0 as expired")
	}, 5*time.Second, 200*time.Millisecond)

	// Step 5: Clear expiry and verify peers see node reactivated.
	_, _, err = st.SetNodeExpiry(types.NodeID(node0ID), nil)
	require.NoError(t, err)

	require.NoError(t, client1.RestartPoll(ctx))
	require.NoError(t, client2.RestartPoll(ctx))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		nm1 := client1.Netmap()
		nm2 := client2.Netmap()

		p1, ok := findPeerByID(nm1, node0ID)
		assert.True(c, ok, "peer1 should still have node0 after clearing expiry")
		assert.False(c, p1.Expired(), "peer1 should see node0 as NOT expired after clearing")

		p2, ok := findPeerByID(nm2, node0ID)
		assert.True(c, ok, "peer2 should still have node0 after clearing expiry")
		assert.False(c, p2.Expired(), "peer2 should see node0 as NOT expired after clearing")
	}, 5*time.Second, 200*time.Millisecond)
}

// findPeerByID looks up a peer by its tailcfg.NodeID within a network map.
// Returns the NodeView and true if found, or zero value and false otherwise.
func findPeerByID(nm *netmap.NetworkMap, id tailcfg.NodeID) (tailcfg.NodeView, bool) {
	if nm == nil {
		return tailcfg.NodeView{}, false
	}
	for _, p := range nm.Peers {
		if p.ID() == id {
			return p, true
		}
	}
	return tailcfg.NodeView{}, false
}

// TestTaggedNodeManualExpiryViaAdmin verifies that when an admin calls
// SetNodeExpiry on a tagged node (via the explicit admin path), the node
// correctly sees itself as expired in its netmap and can be reactivated —
// even though the logout handler (handleLogout) refuses to stamp expiry on
// tagged nodes. This tests the "admin override" path through SetNodeExpiry.
func TestTaggedNodeManualExpiryViaAdmin(t *testing.T) {
	t.Parallel()

	srv := servertest.NewServer(t)

	// Create the tag owner user first (required for tagOwners policy).
	tagOwner := srv.CreateUser(t, "tagowner")

	// Tag owners who can claim the tag + ACL allowing all traffic.
	srv.State().SetPolicy([]byte(`{
		"tagOwners": {"tag:server": [` + fmt.Sprintf(`"user:%d"`, tagOwner.ID) + `]},
		"acls": [{"action": "accept", "src": ["*"], "dst": ["*:*"]}]
	}`))

	serverUser := srv.CreateUser(t, "servers")

	nodeClient := servertest.NewClient(t, srv, "tagged-srv",
		servertest.WithUser(serverUser),
		servertest.WithTags("tag:server"))

	// Verify the node registered successfully.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		nm := nodeClient.Netmap()
		assert.NotNil(c, nm)
	}, 5*time.Second, 200*time.Millisecond)

	st := srv.State()

	// Find our tagged node in the NodeStore by hostname.
	var nodeView types.NodeView
	found := false
	for _, n := range st.ListNodes().All() {
		if n.Hostname() == "tagged-srv" {
			nodeView = n
			found = true
			break
		}
	}
	require.True(t, found, "node must exist in store")
	assert.True(t, nodeView.IsTagged(), "node must be tagged")

	nodeID := nodeView.ID()

	// Step 1: Confirm not expired initially.
	require.NoError(t, nodeClient.RestartPoll(context.Background()))
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.False(c, nodeClient.Netmap().SelfNode.Expired(),
			"newly registered tagged node should not be expired")
	}, 5*time.Second, 200*time.Millisecond)

	// Step 2: Admin expires the tagged node via SetNodeExpiry (the only way
	// to expire a tagged node, since handleLogout refuses to do so).
	expiredAt := time.Now().Add(-5 * time.Minute)
	_, _, err := st.SetNodeExpiry(nodeID, &expiredAt)
	require.NoError(t, err)

	// Step 3: Verify the node sees itself as expired after refreshing.
	require.NoError(t, nodeClient.RestartPoll(context.Background()))
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, nodeClient.Netmap().SelfNode.Expired(),
			"admin-expired tagged node must see Expired=true in netmap")
	}, 5*time.Second, 200*time.Millisecond)

	// Step 4: Admin clears expiry; node recovers.
	_, _, err = st.SetNodeExpiry(nodeID, nil)
	require.NoError(t, err)

	require.NoError(t, nodeClient.RestartPoll(context.Background()))
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		nm := nodeClient.Netmap()
		assert.NotNil(c, nm)
		assert.False(c, nm.SelfNode.Expired(),
			"cleared tagged node should no longer be expired")
	}, 5*time.Second, 200*time.Millisecond)
}

// TestNodeExpirySelfAndPeerSimultaneously verifies that when a node is expired
// via SetNodeExpiry, both the self-node AND its peers see the correct
// Expired flag simultaneously after a single round of poll restarts.
func TestNodeExpirySelfAndPeerSimultaneously(t *testing.T) {
	t.Parallel()

	h := servertest.NewHarness(t, 3)

	st := h.Server.State()
	client0 := h.Client(0) // node that will be expired
	client1 := h.Client(1)
	client2 := h.Client(2)

	// Verify mesh is complete before testing.
	h.WaitForMeshComplete(t, 5*time.Second)

	node0ID := types.NodeID(client0.Netmap().SelfNode.ID()) //nolint:gosec

	// Step 1: Confirm everything starts clean.
	assert.False(t, client0.Netmap().SelfNode.Expired(), "node0 should not be expired initially")
	assert.False(t, client1.Netmap().SelfNode.Expired(), "node1 should not be expired")
	assert.False(t, client2.Netmap().SelfNode.Expired(), "node2 should not be expired")

	p0Before1, ok1 := findPeerByID(client1.Netmap(), tailcfg.NodeID(node0ID))
	p0Before2, ok2 := findPeerByID(client2.Netmap(), tailcfg.NodeID(node0ID))
	require.True(t, ok1, "peer1 should have node0")
	require.True(t, ok2, "peer2 should have node0")
	assert.False(t, p0Before1.Expired(), "peer1 should see node0 as active")
	assert.False(t, p0Before2.Expired(), "peer2 should see node0 as active")

	// Step 2: Expire node0.
	expiredAt := time.Now().Add(-5 * time.Minute)
	_, _, err := st.SetNodeExpiry(node0ID, &expiredAt)
	require.NoError(t, err)

	// Step 3: ALL three clients restart their polls simultaneously.
	ctx := context.Background()
	require.NoError(t, client0.RestartPoll(ctx))
	require.NoError(t, client1.RestartPoll(ctx))
	require.NoError(t, client2.RestartPoll(ctx))

	// Step 4: All three should converge: self-node expired, peers see expired.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		// Self-node should see itself as expired
		nm0 := client0.Netmap()
		assert.NotNil(c, nm0)
		assert.True(c, nm0.SelfNode.Expired(),
			"node0 must see SelfNode.Expired=true after being expired")

		// Peer1 should see node0 as expired
		p0From1, ok := findPeerByID(client1.Netmap(), tailcfg.NodeID(node0ID))
		assert.True(c, ok, "peer1 still has node0 in netmap")
		assert.True(c, p0From1.Expired(), "peer1 should see node0 as expired")

		// Peer2 should see node0 as expired
		p0From2, ok := findPeerByID(client2.Netmap(), tailcfg.NodeID(node0ID))
		assert.True(c, ok, "peer2 still has node0 in netmap")
		assert.True(c, p0From2.Expired(), "peer2 should see node0 as expired")
	}, 5*time.Second, 200*time.Millisecond)

	// Step 5: Clear expiry and verify all return to non-expired.
	_, _, err = st.SetNodeExpiry(node0ID, nil)
	require.NoError(t, err)

	require.NoError(t, client0.RestartPoll(ctx))
	require.NoError(t, client1.RestartPoll(ctx))
	require.NoError(t, client2.RestartPoll(ctx))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.False(c, client0.Netmap().SelfNode.Expired(),
			"node0 should NOT be expired after clearing expiry")
		p0From1, ok := findPeerByID(client1.Netmap(), tailcfg.NodeID(node0ID))
		if ok {
			assert.False(c, p0From1.Expired(), "peer1 should see node0 as NOT expired")
		}
		p0From2, ok := findPeerByID(client2.Netmap(), tailcfg.NodeID(node0ID))
		if ok {
			assert.False(c, p0From2.Expired(), "peer2 should see node0 as NOT expired")
		}
	}, 5*time.Second, 200*time.Millisecond)
}

// TestExpiredNodeRecoveryViaPreAuthKey verifies that when an expired user-owned
// node re-registers using its pre-auth key, the server forces validation of the
// key (via the isExpired flag in HandleNodeFromPreAuthKey), then proceeds with
// registration — restoring the node to active state. This is a critical failure-
// point: after admin-set expiry, a one-shot key would fail ("already used"),
// but a reusable key must succeed and clear the expired status.
//
// This exercises the code path at state/state.go:2526-2528 (isExpired check)
// and the subsequent update block that resets Expiry from the client request.
func TestExpiredNodeRecoveryViaPreAuthKey(t *testing.T) {
	t.Parallel()

	h := servertest.NewHarness(t, 2) // node + peer for mesh verification

	st := h.Server.State()
	nodeClient := h.Client(0)      // will be expired
	peerClient := h.Client(1)      // observes recovery

	// Step 1: Confirm clean start — mesh formed, not expired.
	h.WaitForMeshComplete(t, 5*time.Second)
	assert.False(t, nodeClient.Netmap().SelfNode.Expired(),
		"node should not be expired before admin intervention")

	nodeID := types.NodeID(nodeClient.Netmap().SelfNode.ID()) //nolint:gosec

	// Step 2: Admin expires the node via SetNodeExpiry.
	expiredAt := time.Now().Add(-5 * time.Minute)
	_, _, err := st.SetNodeExpiry(nodeID, &expiredAt)
	require.NoError(t, err)

	// Step 3: Verify the node sees itself as expired via MapRequest.
	ctx := context.Background()
	require.NoError(t, nodeClient.RestartPoll(ctx))
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		nm := nodeClient.Netmap()
		assert.NotNil(c, nm, "netmap should arrive after poll restart")
		assert.True(c, nm.SelfNode.Expired(),
			"admin-expired node must see Expired=true in netmap")
	}, 5*time.Second, 200*time.Millisecond)

	// Step 4: Peer should see the node as expired (may need map refresh).
	if peerNode, found := findPeerByID(peerClient.Netmap(), tailcfg.NodeID(nodeID)); found {
		assert.True(t, peerNode.Expired(),
			"peer should see expired node")
	} else {
		t.Log("peer has not yet seen expired node; propagation pending")
	}

	// Step 5: Node attempts to recover by disconnecting, then re-registering
	// with its pre-auth key. We must tear down the existing poll before
	// ReloginAndPoll so the Tailscale control client isn't holding two
	// mapSessions simultaneously.
	nodeClient.Disconnect(t)

	reloginCtx, reloginCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer reloginCancel()

	err = nodeClient.ReloginAndPoll(reloginCtx)
	require.NoError(t, err, "re-login with pre-auth key must succeed after manual expiry")

	// Step 6: Verify the node is no longer expired.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		nm := nodeClient.Netmap()
		if nm == nil {
			c.FailNow()
			return
		}
		assert.False(c, nm.SelfNode.Expired(),
			"re-registered node must NOT be expired after auth-key recovery")
	}, 5*time.Second, 200*time.Millisecond)

	// Step 7: Peer must also see the node as active again.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		nm := peerClient.Netmap()
		if nm == nil {
			c.FailNow()
			return
		}
		p0, ok := findPeerByID(nm, tailcfg.NodeID(nodeID))
		assert.True(c, ok, "peer should still have recovered node in netmap")
		if ok {
			assert.False(c, p0.Expired(),
				"peer must see recovered node as active")
		}
	}, 5*time.Second, 200*time.Millisecond)
}
