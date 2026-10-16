package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv1 "github.com/tomiwebpro/stealthscale/gen/client/v1"
)

// TestFilterNodesByIdentifier pins the --identifier filter of
// `stscale nodes list-routes`: 0 means "all nodes", a match
// narrows to exactly that node, and a miss is an error — it
// must never fall back to the full list, which would show the
// operator every node's routes when they asked for one node.
func TestFilterNodesByIdentifier(t *testing.T) {
	nodes := []clientv1.Node{
		{Id: "1", GivenName: "myrouter"},  //nolint:exhaustruct_v5 // test fixture: only fields under test
		{Id: "2", GivenName: "plainnode"}, //nolint:exhaustruct_v5 // test fixture: only fields under test
	}

	t.Run("zero returns all", func(t *testing.T) {
		got, err := filterNodesByIdentifier(nodes, 0)
		require.NoError(t, err)
		assert.Len(t, got, 2)
	})

	t.Run("match narrows to one", func(t *testing.T) {
		got, err := filterNodesByIdentifier(nodes, 2)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "plainnode", got[0].GivenName)
	})

	t.Run("miss is an error", func(t *testing.T) {
		_, err := filterNodesByIdentifier(nodes, 999)
		require.ErrorIs(t, err, errNodeNotFound)
		assert.Contains(t, err.Error(), "999")
	})

	t.Run("miss on empty list is an error", func(t *testing.T) {
		_, err := filterNodesByIdentifier(nil, 1)
		require.Error(t, err)
	})

	t.Run("zero on empty list is empty", func(t *testing.T) {
		got, err := filterNodesByIdentifier(nil, 0)
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}
