package servertest_test

import (
	"context"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tomiwebpro/stealthscale/hscontrol/servertest"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
	"tailscale.com/tailcfg"
	"tailscale.com/types/netmap"
)

// TestEndpointUpdateViaLiteMapRequest pins the endpoint-update
// failure-point: a node pushes newly discovered endpoints with a
// lite map request (Stream=false, OmitPeers=true — the shape
// controlclient.Direct.SendUpdate emits) without disturbing its
// streaming long-poll session. The endpoints must land in the
// NodeStore, fan out to peers as an incremental peer patch (the
// delta adds a useful, non-STUN endpoint, so it is
// broadcast-worthy), and the pre-existing streaming session must
// keep delivering updates afterwards.
func TestEndpointUpdateViaLiteMapRequest(t *testing.T) {
	t.Parallel()

	srv := servertest.NewServer(t)
	user := srv.CreateUser(t, "ep-user")

	c1 := servertest.NewClient(t, srv, "ep-node1", servertest.WithUser(user))
	c2 := servertest.NewClient(t, srv, "ep-node2", servertest.WithUser(user))

	c1.WaitForPeers(t, 1, 10*time.Second)
	c2.WaitForPeers(t, 1, 10*time.Second)

	nodeID := types.NodeID(c1.Netmap().SelfNode.ID()) //nolint:gosec // NodeID is bounded

	// A newly discovered, useful (non-STUN) endpoint: the kind of
	// delta netcheck pushes once it has mapped the NAT.
	newEP := netip.MustParseAddrPort("192.168.1.10:41641")

	c1.Direct().SetEndpoints([]tailcfg.Endpoint{
		{Addr: newEP, Type: tailcfg.EndpointLocal},
	})

	// The lite update itself. The server must answer 200 OK (the
	// client only checks the status code — the response body is
	// intentionally empty for endpoint updates) and process the
	// endpoints without tearing down the streaming poll.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	require.NoError(
		t,
		c1.Direct().SendUpdate(ctx),
		"lite endpoint update must succeed with 200 OK",
	)

	// The endpoints landed in the NodeStore.
	updated, ok := srv.State().GetNodeByID(nodeID)
	require.True(t, ok, "node must exist in the NodeStore after the lite update")

	assert.Contains(
		t,
		updated.Endpoints().AsSlice(),
		newEP,
		"the lite update's endpoint must be stored on the node",
	)

	// The delta is broadcast-worthy, so the peer's netmap must
	// carry the new endpoint.
	c2.WaitForCondition(t, "peer endpoint propagated", 10*time.Second, func(nm *netmap.NetworkMap) bool {
		for _, p := range nm.Peers {
			hi := p.Hostinfo()
			if !hi.Valid() || hi.Hostname() != "ep-node1" {
				continue
			}

			if slices.Contains(p.Endpoints().AsSlice(), newEP) {
				return true
			}
		}

		return false
	})

	// The streaming long-poll survived the concurrent lite
	// update: a later topology change (a third node joining
	// the tailnet) must still reach c1 through its original
	// session. If the endpoint update had wedged or replaced
	// the streaming session, c1 would never learn about the
	// new peer.
	c3 := servertest.NewClient(t, srv, "ep-node3", servertest.WithUser(user))

	c3.WaitForPeers(t, 2, 10*time.Second)
	c1.WaitForPeers(t, 2, 10*time.Second)
}
