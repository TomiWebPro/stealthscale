package state

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
)

// TestStateExpireExpiredNodes pins the ticker-driven expiry sweep
// (node expiry/re-auth failure point): only nodes that expired since
// the last check produce a KeyExpiry change. A node that expired long
// ago must not re-notify on every tick, and healthy nodes stay
// silent — otherwise peers get spammed with duplicate key-expiry
// patches and the ticker never converges.
func TestStateExpireExpiredNodes(t *testing.T) {
	newStateWithNode := func(t *testing.T, expiry *time.Time) *State {
		t.Helper()

		s, err := NewState(persistTestConfig(t.TempDir() + "/expire-sweep.db"))
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })

		node := createTestNode(1, 1, "user1", "node1")
		node.Expiry = expiry
		s.nodeStore.PutNode(node)

		return s
	}

	t.Run("freshly expired node notifies once", func(t *testing.T) {
		expiredAt := time.Now().Add(-time.Minute)
		s := newStateWithNode(t, &expiredAt)

		before := time.Now()
		next, updates, found := s.ExpireExpiredNodes(before.Add(-5 * time.Minute))

		require.True(t, found, "freshly expired node must be reported")
		require.Len(t, updates, 1)
		require.Len(t, updates[0].PeerPatches, 1)
		require.Equal(
			t,
			types.NodeID(1).NodeID(),
			updates[0].PeerPatches[0].NodeID,
		)
		require.NotNil(t, updates[0].PeerPatches[0].KeyExpiry)
		require.True(
			t,
			updates[0].PeerPatches[0].KeyExpiry.Equal(expiredAt),
			"KeyExpiry = %v, want %v",
			updates[0].PeerPatches[0].KeyExpiry,
			expiredAt,
		)
		require.False(t, next.Before(before), "next check must not go backwards")
	})

	t.Run("stale expiry does not re-notify", func(t *testing.T) {
		expiredAt := time.Now().Add(-time.Hour)
		s := newStateWithNode(t, &expiredAt)

		// The node expired before the last check — a previous tick
		// already reported it, so this tick must stay silent.
		_, updates, found := s.ExpireExpiredNodes(time.Now())

		require.False(t, found, "long-expired node must not re-notify")
		require.Empty(t, updates)
	})

	t.Run("unexpired node stays silent", func(t *testing.T) {
		expiry := time.Now().Add(time.Hour)
		s := newStateWithNode(t, &expiry)

		_, updates, found := s.ExpireExpiredNodes(time.Now().Add(-time.Minute))

		require.False(t, found)
		require.Empty(t, updates)
	})

	t.Run("node without expiry stays silent", func(t *testing.T) {
		s := newStateWithNode(t, nil)

		_, updates, found := s.ExpireExpiredNodes(time.Now().Add(-time.Minute))

		require.False(t, found)
		require.Empty(t, updates)
	})

	t.Run("empty store stays silent", func(t *testing.T) {
		s, err := NewState(persistTestConfig(t.TempDir() + "/expire-sweep-empty.db"))
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })

		_, updates, found := s.ExpireExpiredNodes(time.Now().Add(-time.Minute))

		require.False(t, found)
		require.Empty(t, updates)
	})
}
