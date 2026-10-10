package servertest_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tomiwebpro/stealthscale/hscontrol/servertest"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
	"tailscale.com/control/controlclient"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

// TestNodeKeyRotationViaPreAuthKey exercises the node-key rotation
// failure mode end to end: a client that re-registers with a NEW
// NodeKey (same machine key, same reusable pre-auth key) must be
// re-authorised, its stored NodeKey swapped in place, and no
// duplicate node created.
//
// The rotation is forced the way a real client does it:
// controlclient.Direct regenerates its node key when TryLogin runs
// with LoginInteractive (or after the old key expired), which makes
// HandleNodeFromPreAuthKey take the isNodeKeyRotation branch — the
// path that must not ride the skip-validation fast path even though
// the machine key matches.
func TestNodeKeyRotationViaPreAuthKey(t *testing.T) {
	t.Parallel()

	h := servertest.NewHarness(t, 2)

	st := h.Server.State()
	nodeClient := h.Client(0)
	peerClient := h.Client(1)

	h.WaitForMeshComplete(t, 5*time.Second)

	nodeID := types.NodeID(nodeClient.Netmap().SelfNode.ID()) //nolint:gosec // NodeID is bounded
	before, ok := st.GetNodeByID(nodeID)
	require.True(t, ok, "node must exist in the NodeStore before rotation")

	oldNodeKey := before.NodeKey()
	machineKey := nodeClient.MachineKey().Public()
	require.Equal(t, machineKey, before.MachineKey())

	// Both harness clients share the default user, so count the
	// user's nodes before rotating to detect any duplicate the
	// rotation might create.
	nodesBefore := st.ListNodesByUser(types.UserID(h.DefaultUser().ID)).Len()

	// Tear down the poll session so the control client does not hold
	// two map sessions across the re-login (same pattern as
	// ReloginAndPoll).
	nodeClient.Disconnect(t)

	// LoginInteractive forces Direct to generate a fresh NodeKey and
	// re-register with the same machine key and the same reusable
	// pre-auth key — the shape of `tailscale up` after state loss.
	loginCtx, loginCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer loginCancel()

	authURL, err := nodeClient.Direct().TryLogin(loginCtx, controlclient.LoginInteractive)
	require.NoError(t, err, "rotation re-registration with a valid reusable key must succeed")
	require.Empty(t, authURL, "rotation with a pre-auth key must not require interactive auth")

	// The server must have swapped the node key in place: same node
	// ID, new node key, same machine key, and still exactly one node
	// for the user (rotation must not create a duplicate).
	after, ok := st.GetNodeByID(nodeID)
	require.True(t, ok, "rotated node must keep its node ID")

	assert.NotEqual(t, oldNodeKey, after.NodeKey(), "rotation must replace the stored NodeKey")
	assert.Equal(t, machineKey, after.MachineKey(), "rotation must keep the machine-key identity")
	assert.False(t, after.IsExpired(), "rotated node must not be expired")
	assert.Equal(
		t,
		nodesBefore,
		st.ListNodesByUser(types.UserID(h.DefaultUser().ID)).Len(),
		"rotation must not create a duplicate node for the user",
	)

	// Resume polling and verify the mesh heals and the peer sees the
	// rotated node carrying its new key.
	nodeClient.Reconnect(t)
	h.WaitForMeshComplete(t, 5*time.Second)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		peer, found := peerClient.PeerByName(nodeClient.Name)
		assert.True(c, found, "peer must still see the rotated node")

		if found {
			assert.Equal(
				c,
				after.NodeKey(),
				peer.Key(),
				"peer's netmap must carry the rotated node key",
			)
		}
	}, 5*time.Second, 200*time.Millisecond)
}

// TestNodeKeyRotationRequiresValidKey pins the security boundary
// behind the rotation check: because an isNodeKeyRotation
// re-registration bypasses the skip-validation fast path, a node that
// rotates its NodeKey must present a valid pre-auth key. A spent
// one-shot key — acceptable for a same-node-key re-registration
// (container restart, #2830) — must be rejected when the client also
// rotates its node key, and the rejection must leave the stored node
// untouched.
func TestNodeKeyRotationRequiresValidKey(t *testing.T) {
	t.Parallel()

	server := servertest.NewServer(t)
	st := server.State()

	user := server.CreateUser(t, "rotator")
	uid := types.UserID(user.ID)

	machineKey := key.NewMachine().Public()
	nodeKey1 := key.NewNode().Public()
	nodeKey2 := key.NewNode().Public()

	oneShot, err := st.CreatePreAuthKey(&uid, false, false, nil, nil)
	require.NoError(t, err)

	regReq := func(nodeKey key.NodePublic, authKey string) tailcfg.RegisterRequest {
		return tailcfg.RegisterRequest{ //nolint:exhaustruct_v5 // test fixture: only fields under test
			NodeKey:  nodeKey,
			Hostinfo: &tailcfg.Hostinfo{Hostname: "rotator"},          //nolint:exhaustruct_v5 // test fixture
			Auth:     &tailcfg.RegisterResponseAuth{AuthKey: authKey}, //nolint:exhaustruct_v5 // test fixture
		}
	}

	// Initial registration consumes the one-shot key.
	node, _, err := st.HandleNodeFromPreAuthKey(regReq(nodeKey1, oneShot.Key), machineKey)
	require.NoError(t, err, "initial registration with a fresh one-shot key must succeed")

	// The key is now spent. Rotating the node key with it must fail:
	// the rotation path forces pre-auth key validation, so the
	// boundary cannot be dodged by rotating.
	_, _, err = st.HandleNodeFromPreAuthKey(regReq(nodeKey2, oneShot.Key), machineKey)
	require.Error(t, err, "rotation with a spent one-shot key must be rejected")
	require.ErrorContains(t, err, "authkey already used")

	// The rejected rotation must not have partially applied.
	stored, ok := st.GetNodeByID(node.ID())
	require.True(t, ok, "node must still exist after the rejected rotation")
	assert.Equal(t, nodeKey1, stored.NodeKey(), "rejected rotation must leave the stored node key unchanged")

	// Positive control: the same rotation shape with a valid reusable
	// key succeeds, updates the node in place, and emits the minimal
	// peer patch carrying the new key.
	reusable, err := st.CreatePreAuthKey(&uid, true, false, nil, nil)
	require.NoError(t, err)

	rotated, rotatedChange, err := st.HandleNodeFromPreAuthKey(regReq(nodeKey2, reusable.Key), machineKey)
	require.NoError(t, err, "rotation with a valid reusable key must succeed")
	assert.Equal(t, node.ID(), rotated.ID(), "rotation must update the node in place, not create a new one")
	assert.Equal(t, nodeKey2, rotated.NodeKey())

	require.Len(t, rotatedChange.PeerPatches, 1, "rotation must be advertised as a minimal peer patch")

	patch := rotatedChange.PeerPatches[0]
	assert.Equal(t, tailcfg.NodeID(node.ID()), patch.NodeID) //nolint:gosec // NodeID is bounded
	assert.NotNil(t, patch.Key)
	assert.Equal(t, nodeKey2, *patch.Key)

	stored, ok = st.GetNodeByID(node.ID())
	require.True(t, ok)
	assert.Equal(t, nodeKey2, stored.NodeKey(), "successful rotation must persist the new node key")
}
