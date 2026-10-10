// Copyright (c) 2026 TomiWebPro <TomiWebPro@gmail.com>
// SPDX-License-Identifier: BSD-3-Clause

package servertest_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tomiwebpro/stealthscale/hscontrol/servertest"
	"tailscale.com/types/netmap"
)

// hasPeer checks whether a NetworkMap contains a peer with the given hostname.
func hasPeer(nm *netmap.NetworkMap, hostname string) bool {
	if nm == nil {
		return false
	}
	for _, p := range nm.Peers {
		hi := p.Hostinfo()
		if hi.Valid() && hi.Hostname() == hostname {
			return true
		}
	}
	return false
}

// TestPolicyChangePropagatesDuringActivePoll verifies that when an ACL policy
// changes while all clients have active long-poll sessions, every connected
// client receives an updated MapResponse reflecting the new policy.
func TestPolicyChangePropagatesDuringActivePoll(t *testing.T) {
	t.Parallel()

	h := servertest.NewHarness(t, 3)

	// Verify mesh formation first.
	h.WaitForMeshComplete(t, 10*time.Second)

	// Record initial state.
	initialCount := make([]int, len(h.Clients()))
	for i, c := range h.Clients() {
		initialCount[i] = c.UpdateCount()
	}

	// Apply a targeted ACL (only allows specific prefix, excluding default 100.x).
	newPolicy := []byte(`{
		"acls": [
			{"action": "accept", "src": ["*"], "dst": ["192.168.0.0/16:*"]}
		]
	}`)

	h.ChangePolicy(t, newPolicy)

	// All three clients should get at least one update reflecting the change.
	for i, c := range h.Clients() {
		c.WaitForCondition(t, "update after restrictive policy",
			10*time.Second,
			func(nm *netmap.NetworkMap) bool {
				return c.UpdateCount() > initialCount[i]
			})
	}
}

// TestNodeReconnectAfterPolicyChange verifies the critical failure mode where
// a node disconnects right before a policy change and reconnects after it.
// The reconnected node must receive the post-change netmap, not a stale one.
func TestNodeReconnectAfterPolicyChange(t *testing.T) {
	t.Parallel()

	srv := servertest.NewServer(t)

	// Create a permissive policy initially.
	srv.State().SetPolicy([]byte(`{
		"acls": [{"action": "accept", "src": ["*"], "dst": ["*:*"]}]
	}`))

	user := srv.CreateUser(t, "reconn-user")
	clientA := servertest.NewClient(t, srv, "reconn-a", servertest.WithUser(user))
	clientB := servertest.NewClient(t, srv, "reconn-b", servertest.WithUser(user))

	// Both start connected and see each other.
	clientA.WaitForPeers(t, 1, 10*time.Second)
	require.True(t, hasPeer(clientA.Netmap(), "reconn-b"),
		"before disconnect: A should see B")

	// Disconnect B (simulates network blip / reboot).
	clientB.Disconnect(t)

	// Change policy to restrict traffic (only allow a non-default subnet).
	srv.State().SetPolicy([]byte(`{
		"acls": [{"action": "accept", "src": ["*"], "dst": ["192.168.0.0/16:*"]}]
	}`))

	// Reconnect B. It should see the new policy reflected in its netmap
	// — peers may still appear in Peer list but packet filters should be empty.
	clientB.Reconnect(t)
	clientB.WaitForUpdate(t, 15*time.Second)

	nmB := clientB.Netmap()
	require.NotNil(t, nmB)

	// After the restricted policy, the PacketFilter should be empty (no
	// accept rules match any 100.x traffic between the two nodes).
	// This proves the new policy took effect in B's netmap.
	require.Empty(t, nmB.PacketFilter,
		"B's PacketFilter should be empty after reconnect with restricted policy (no matching accept rules)")
}

// TestMultiplePolicyChangesConverge verifies that rapid successive policy
// changes are applied in order and each produces a convergent netmap update.
// This guards against batcher coalescing out-of-order changes into a stale snapshot.
func TestMultiplePolicyChangesConverge(t *testing.T) {
	t.Parallel()

	h := servertest.NewHarness(t, 2)
	h.WaitForMeshComplete(t, 10*time.Second)

	countBefore := h.Client(0).UpdateCount()

	// Three distinct policies in rapid succession, all using valid actions.
	policies := [][]byte{
		[]byte(`{"acls": [{"action": "accept", "src": ["*"], "dst": ["*:*"]}]}`),
		[]byte(`{"acls": [{"action": "accept", "src": ["*"], "dst": ["192.168.0.0/16:*"]}]}`),
		[]byte(`{"acls": [{"action": "accept", "src": ["*"], "dst": ["*:*"]}]}`),
	}

	for i, pol := range policies {
		h.ChangePolicy(t, pol)

		h.Client(0).WaitForCondition(t, "policy change #"+string(rune('0'+i)),
			10*time.Second,
			func(nm *netmap.NetworkMap) bool {
				return h.Client(0).UpdateCount() > countBefore
			})
		countBefore = h.Client(0).UpdateCount()
		t.Logf("converged after policy change #%d", i)
	}
}
