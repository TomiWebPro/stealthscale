package state

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

// TestRegistrationRejectsRevokedPreAuthKey pins revocation
// enforcement at the registration boundary. The v2 API tests
// (apiv2_keys_test.go, servertest/apiv2_test.go) only assert that
// RevokePreAuthKey stamps the key's Revoked timestamp; no test
// drove a revoked key back through HandleNodeFromPreAuthKey, so
// the revoked branch of PreAuthKey.Validate ("authkey revoked")
// was unexercised end to end. Revocation is only meaningful if
// the join path enforces it: an operator who revokes a leaked key
// expects the machine it authorized to be unable to join with it
// — including rotating its node key (--force-reauth) or re-tagging
// itself with a second revoked key — after the fact.
func TestRegistrationRejectsRevokedPreAuthKey(t *testing.T) {
	t.Run("fresh join", func(t *testing.T) {
		s := newRetagTestState(t)

		user := s.CreateUserForTest("revoked-join-user")

		pak, err := s.CreatePreAuthKey(user.TypedID(), false, false, nil, nil)
		require.NoError(t, err)

		require.NoError(t, s.RevokePreAuthKey(pak.ID))

		machineKey := key.NewMachine()

		_, _, err = s.HandleNodeFromPreAuthKey(tailcfg.RegisterRequest{
			Auth:     &tailcfg.RegisterResponseAuth{AuthKey: pak.Key},
			NodeKey:  key.NewNode().Public(),
			Hostinfo: &tailcfg.Hostinfo{Hostname: "revoked-join-node"},
			Expiry:   time.Now().Add(24 * time.Hour),
		}, machineKey.Public())
		require.ErrorContains(t, err, "authkey revoked",
			"a revoked pre-auth key must not register a new node")

		require.Empty(t, s.nodeStore.GetNodesByMachineKeyAllUsers(machineKey.Public()),
			"a rejected join must not create a node")
	})

	t.Run("node key rotation", func(t *testing.T) {
		s := newRetagTestState(t)

		user := s.CreateUserForTest("revoked-rotation-user")

		key1, err := s.CreatePreAuthKey(user.TypedID(), false, false, nil, nil)
		require.NoError(t, err)

		machineKey := key.NewMachine()
		nodeKey := key.NewNode().Public()

		regReq := tailcfg.RegisterRequest{
			Auth:     &tailcfg.RegisterResponseAuth{AuthKey: key1.Key},
			NodeKey:  nodeKey,
			Hostinfo: &tailcfg.Hostinfo{Hostname: "revoked-rotation-node"},
			Expiry:   time.Now().Add(24 * time.Hour),
		}

		first, _, err := s.HandleNodeFromPreAuthKey(regReq, machineKey.Public())
		require.NoError(t, err)

		// A second key, revoked before use, presented with a rotated
		// node key (the --force-reauth shape): the rotation path must
		// validate the key and reject.
		key2, err := s.CreatePreAuthKey(user.TypedID(), false, false, nil, nil)
		require.NoError(t, err)

		require.NoError(t, s.RevokePreAuthKey(key2.ID))

		rotation := regReq
		rotation.Auth = &tailcfg.RegisterResponseAuth{AuthKey: key2.Key}
		rotation.NodeKey = key.NewNode().Public()

		_, _, err = s.HandleNodeFromPreAuthKey(rotation, machineKey.Public())
		require.ErrorContains(t, err, "authkey revoked",
			"a revoked pre-auth key must not authorize a node-key rotation")

		// The node keeps its original node key; the rotated key is
		// not bound to the machine.
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

		first, _, err := s.HandleNodeFromPreAuthKey(tailcfg.RegisterRequest{
			Auth:     &tailcfg.RegisterResponseAuth{AuthKey: key1.Key},
			NodeKey:  nodeKey,
			Hostinfo: &tailcfg.Hostinfo{Hostname: "revoked-retag-node"},
		}, machineKey.Public())
		require.NoError(t, err)
		require.Equal(t, []string{"tag:tag1"}, first.Tags().AsSlice())

		// KEY2: a different tagged key, revoked before use. Presenting
		// it for the same machine is a retag, which must validate the
		// key rather than ride the skip-validation fast path.
		key2, err := s.CreatePreAuthKey(nil, false, false, nil, []string{"tag:tag2"})
		require.NoError(t, err)

		require.NoError(t, s.RevokePreAuthKey(key2.ID))

		reReg := tailcfg.RegisterRequest{
			Auth:     &tailcfg.RegisterResponseAuth{AuthKey: key2.Key},
			NodeKey:  nodeKey,
			Hostinfo: &tailcfg.Hostinfo{Hostname: "revoked-retag-node"},
		}

		_, _, err = s.HandleNodeFromPreAuthKey(reReg, machineKey.Public())
		require.ErrorContains(t, err, "authkey revoked",
			"a revoked tagged key must not retag a node")

		after, ok := s.GetNodeByID(first.ID())
		require.True(t, ok)
		require.Equal(t, []string{"tag:tag1"}, after.Tags().AsSlice(),
			"a rejected retag must not apply the revoked key's tags")
	})
}
