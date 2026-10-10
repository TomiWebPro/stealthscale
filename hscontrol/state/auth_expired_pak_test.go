package state

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

// TestRegistrationRejectsExpiredPreAuthKey pins expiry
// enforcement at the registration boundary — the expired-key
// companion to TestRegistrationRejectsRevokedPreAuthKey.
//
// The hscontrol-level tests (auth_test.go) only drive a fresh
// join with an expired key through handleRegister; no test
// drove an expired key back through HandleNodeFromPreAuthKey
// on the paths that bypass the skip-validation fast path
// (node-key rotation, retag, expired-node re-auth). Expiry is
// only meaningful if the join path enforces it on every
// re-validation boundary: an operator who sets a key expiry
// (or whose key ages past it) expects the key to authorize
// nothing — not a rotation (--force-reauth), not a retag, not
// a waking expired node — after the fact, and the boundary
// must not depend on the client rotating its node key.
func TestRegistrationRejectsExpiredPreAuthKey(t *testing.T) {
	t.Run("fresh join", func(t *testing.T) {
		s := newRetagTestState(t)

		user := s.CreateUserForTest("expired-join-user")

		past := time.Now().Add(-time.Hour)
		pak, err := s.CreatePreAuthKey(user.TypedID(), false, false, &past, nil)
		require.NoError(t, err)

		machineKey := key.NewMachine()

		_, _, err = s.HandleNodeFromPreAuthKey(tailcfg.RegisterRequest{ //nolint:exhaustruct_v5 // test fixture: only fields under test
			Auth:     &tailcfg.RegisterResponseAuth{AuthKey: pak.Key}, //nolint:exhaustruct_v5 // test fixture
			NodeKey:  key.NewNode().Public(),
			Hostinfo: &tailcfg.Hostinfo{Hostname: "expired-join-node"}, //nolint:exhaustruct_v5 // test fixture
			Expiry:   time.Now().Add(24 * time.Hour),
		}, machineKey.Public())
		require.ErrorContains(t, err, "authkey expired",
			"an expired pre-auth key must not register a new node")

		require.Empty(t, s.nodeStore.GetNodesByMachineKeyAllUsers(machineKey.Public()),
			"a rejected join must not create a node")
	})

	t.Run("node key rotation", func(t *testing.T) {
		s := newRetagTestState(t)

		user := s.CreateUserForTest("expired-rotation-user")

		key1, err := s.CreatePreAuthKey(user.TypedID(), false, false, nil, nil)
		require.NoError(t, err)

		machineKey := key.NewMachine()
		nodeKey := key.NewNode().Public()

		regReq := tailcfg.RegisterRequest{ //nolint:exhaustruct_v5 // test fixture: only fields under test
			Auth:     &tailcfg.RegisterResponseAuth{AuthKey: key1.Key}, //nolint:exhaustruct_v5 // test fixture
			NodeKey:  nodeKey,
			Hostinfo: &tailcfg.Hostinfo{Hostname: "expired-rotation-node"}, //nolint:exhaustruct_v5 // test fixture
			Expiry:   time.Now().Add(24 * time.Hour),
		}

		first, _, err := s.HandleNodeFromPreAuthKey(regReq, machineKey.Public())
		require.NoError(t, err, "initial registration with a fresh key must succeed")

		// A second key, already expired, presented with a
		// rotated node key (the --force-reauth shape): the
		// rotation path must validate the key and reject.
		past := time.Now().Add(-time.Hour)
		key2, err := s.CreatePreAuthKey(user.TypedID(), false, false, &past, nil)
		require.NoError(t, err)

		rotation := regReq
		rotation.Auth = &tailcfg.RegisterResponseAuth{AuthKey: key2.Key} //nolint:exhaustruct_v5 // test fixture
		rotation.NodeKey = key.NewNode().Public()

		_, _, err = s.HandleNodeFromPreAuthKey(rotation, machineKey.Public())
		require.ErrorContains(t, err, "authkey expired",
			"an expired pre-auth key must not authorize a node-key rotation")

		// The node keeps its original node key; the rotated key
		// is not bound to the machine.
		owner, ok := s.GetNodeByNodeKey(nodeKey)
		require.True(t, ok)
		require.Equal(t, machineKey.Public(), owner.MachineKey())
		require.Equal(t, first.ID(), owner.ID())

		_, ok = s.GetNodeByNodeKey(rotation.NodeKey)
		require.False(t, ok, "the rejected rotation must not claim the new node key")
	})

	t.Run("retag", func(t *testing.T) {
		s := newRetagTestState(t)

		// KEY1: single-use tagged key, registers the node.
		key1, err := s.CreatePreAuthKey(nil, false, false, nil, []string{"tag:tag1"})
		require.NoError(t, err)

		machineKey := key.NewMachine()
		nodeKey := key.NewNode().Public()

		first, _, err := s.HandleNodeFromPreAuthKey(tailcfg.RegisterRequest{ //nolint:exhaustruct_v5 // test fixture: only fields under test
			Auth:     &tailcfg.RegisterResponseAuth{AuthKey: key1.Key}, //nolint:exhaustruct_v5 // test fixture
			NodeKey:  nodeKey,
			Hostinfo: &tailcfg.Hostinfo{Hostname: "expired-retag-node"}, //nolint:exhaustruct_v5 // test fixture
		}, machineKey.Public())
		require.NoError(t, err)
		require.Equal(t, []string{"tag:tag1"}, first.Tags().AsSlice())

		// KEY2: a different tagged key, already expired.
		// Presenting it for the same machine is a retag, which
		// must validate the key rather than ride the
		// skip-validation fast path.
		past := time.Now().Add(-time.Hour)
		key2, err := s.CreatePreAuthKey(nil, false, false, &past, []string{"tag:tag2"})
		require.NoError(t, err)

		reReg := tailcfg.RegisterRequest{ //nolint:exhaustruct_v5 // test fixture: only fields under test
			Auth:     &tailcfg.RegisterResponseAuth{AuthKey: key2.Key}, //nolint:exhaustruct_v5 // test fixture
			NodeKey:  nodeKey,
			Hostinfo: &tailcfg.Hostinfo{Hostname: "expired-retag-node"}, //nolint:exhaustruct_v5 // test fixture
		}

		_, _, err = s.HandleNodeFromPreAuthKey(reReg, machineKey.Public())
		require.ErrorContains(t, err, "authkey expired",
			"an expired tagged key must not retag a node")

		after, ok := s.GetNodeByID(first.ID())
		require.True(t, ok)
		require.Equal(t, []string{"tag:tag1"}, after.Tags().AsSlice(),
			"a rejected retag must not apply the expired key's tags")
	})

	t.Run("expired node re-auth", func(t *testing.T) {
		s := newRetagTestState(t)

		user := s.CreateUserForTest("expired-reauth-user")

		key1, err := s.CreatePreAuthKey(user.TypedID(), false, false, nil, nil)
		require.NoError(t, err)

		machineKey := key.NewMachine()
		nodeKey := key.NewNode().Public()

		first, _, err := s.HandleNodeFromPreAuthKey(tailcfg.RegisterRequest{ //nolint:exhaustruct_v5 // test fixture: only fields under test
			Auth:     &tailcfg.RegisterResponseAuth{AuthKey: key1.Key}, //nolint:exhaustruct_v5 // test fixture
			NodeKey:  nodeKey,
			Hostinfo: &tailcfg.Hostinfo{Hostname: "expired-reauth-node"}, //nolint:exhaustruct_v5 // test fixture
			Expiry:   time.Now().Add(24 * time.Hour),
		}, machineKey.Public())
		require.NoError(t, err)
		require.False(t, first.IsTagged(), "precondition: node must be user-owned")

		// Force the node into the expired (logged-out) state.
		nodeExpiry := time.Now().Add(-time.Hour)

		_, ok := s.nodeStore.UpdateNode(first.ID(), func(n *types.Node) {
			n.Expiry = &nodeExpiry
		})
		require.True(t, ok)

		forced, ok := s.GetNodeByID(first.ID())
		require.True(t, ok)
		require.True(t, forced.IsExpired(), "precondition: node must be expired")

		// An expired node is genuinely re-authenticating, so it
		// must present a valid key — and the boundary must not
		// depend on the client rotating its node key. A second,
		// already-expired key presented with the SAME node key
		// must be rejected.
		past := time.Now().Add(-time.Hour)
		key2, err := s.CreatePreAuthKey(user.TypedID(), false, false, &past, nil)
		require.NoError(t, err)

		reReg := tailcfg.RegisterRequest{ //nolint:exhaustruct_v5 // test fixture: only fields under test
			Auth:     &tailcfg.RegisterResponseAuth{AuthKey: key2.Key}, //nolint:exhaustruct_v5 // test fixture
			NodeKey:  nodeKey,
			Hostinfo: &tailcfg.Hostinfo{Hostname: "expired-reauth-node"}, //nolint:exhaustruct_v5 // test fixture
			Expiry:   time.Now().Add(24 * time.Hour),
		}

		_, _, err = s.HandleNodeFromPreAuthKey(reReg, machineKey.Public())
		require.ErrorContains(t, err, "authkey expired",
			"an expired node re-authenticating must present a valid key, not an expired one")

		after, ok := s.GetNodeByID(first.ID())
		require.True(t, ok)
		require.Equal(t, nodeKey, after.NodeKey(),
			"a rejected re-auth must leave the stored node key unchanged")
	})
}
