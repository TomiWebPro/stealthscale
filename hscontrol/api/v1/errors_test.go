package apiv1

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tomiwebpro/stealthscale/hscontrol/db"
	"github.com/tomiwebpro/stealthscale/hscontrol/state"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
	"gorm.io/gorm"
)

// Static errors for the unmapped-cause cases (err113: never
// construct dynamic errors inline).
var (
	errTestBoom   = errors.New("boom")
	errTestDBDown = errors.New("db down")
)

// statusOf extracts the HTTP status a mapError result will produce.
func statusOf(t *testing.T, err error) int {
	t.Helper()

	var em *huma.ErrorModel
	require.ErrorAsf(t, err, &em, "expected a *huma.ErrorModel, got %T", err)

	return em.Status
}

// TestMapErrorTranslatesSentinels pins the state/db error-to-status
// contract the v1 handlers rely on: a delete race that surfaces a
// 500 instead of a 404 turns a recoverable "resource gone" into a
// retryable server error. Every sentinel in the v1 mapping table is
// covered, in both bare and wrapped form (state wraps sentinels with
// %w before returning them, e.g. fmt.Errorf("renaming node: %w", ...)).
func TestMapErrorTranslatesSentinels(t *testing.T) {
	t.Parallel()

	// Mirrors the wrap shapes state produces: SetNodeExpiry wraps the
	// sentinel with the node id, then the handler adds its context.
	wrappedNodeStoreMiss := fmt.Errorf("expiring node: %w",
		fmt.Errorf("%w: %d", state.ErrNodeNotInNodeStore, types.NodeID(7)))
	wrappedRenameCollision := fmt.Errorf("renaming node: %w",
		fmt.Errorf("%w: %s", state.ErrNodeNameNotUnique, "dup"))
	wrappedUnrelated := fmt.Errorf("expiring node: %w", errTestDBDown)

	tests := []struct {
		name string
		err  error
		want int
	}{
		// Not found: the resource is gone.
		{"gorm record not found", gorm.ErrRecordNotFound, http.StatusNotFound},
		{"node not found", state.ErrNodeNotFound, http.StatusNotFound},
		// The node vanished between the handler's lookup and the state
		// mutation (delete race): the same 404, not a 500.
		{"node left the NodeStore", state.ErrNodeNotInNodeStore, http.StatusNotFound},
		{"user not found", db.ErrUserNotFound, http.StatusNotFound},
		{"registration cache miss", db.ErrNodeNotFoundRegistrationCache, http.StatusNotFound},
		{"registration expired", state.ErrRegistrationExpired, http.StatusNotFound},

		// Invalid input: the caller can fix this request.
		{"given name invalid", state.ErrGivenNameInvalid, http.StatusBadRequest},
		{"given name taken", state.ErrGivenNameTaken, http.StatusBadRequest},
		{"node name not unique", state.ErrNodeNameNotUnique, http.StatusBadRequest},
		{"tagged node without tags", state.ErrNodeMarkedTaggedButHasNoTags, http.StatusBadRequest},
		{"node has neither user nor tags", state.ErrNodeHasNeitherUserNorTags, http.StatusBadRequest},
		{"requested tags invalid", state.ErrRequestedTagsInvalidOrNotPermitted, http.StatusBadRequest},
		{"user still has nodes", db.ErrUserStillHasNodes, http.StatusBadRequest},
		{"cannot change OIDC user", db.ErrCannotChangeOIDCUser, http.StatusBadRequest},
		{"preauth key not tagged or owned", db.ErrPreAuthKeyNotTaggedOrOwned, http.StatusBadRequest},
		{"single-use auth key reused", db.ErrSingleUseAuthKeyHasBeenUsed, http.StatusBadRequest},

		// Conflicts: the request is valid but collides with live state.
		{"node key in use", state.ErrNodeKeyInUse, http.StatusConflict},
		{"ambiguous node ownership", state.ErrAmbiguousNodeOwnership, http.StatusConflict},

		// Anything unmapped is a server fault.
		{"unmapped error", errTestBoom, http.StatusInternalServerError},

		// Wrapped forms: state wraps every sentinel with %w context.
		{"wrapped node store miss", wrappedNodeStoreMiss, http.StatusNotFound},
		{"wrapped rename collision", wrappedRenameCollision, http.StatusBadRequest},
		{"wrapped unrelated cause", wrappedUnrelated, http.StatusInternalServerError},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, statusOf(t, mapError("doing thing", tc.err)))
		})
	}

	t.Run("nil passes through", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, mapError("doing thing", nil))
	})

	// The wrapped sentinel's text must survive into the error detail:
	// clients and logs see the cause, not just the status.
	t.Run("wrapped cause is preserved", func(t *testing.T) {
		t.Parallel()

		err := mapError("expiring node",
			fmt.Errorf("expiring node: %w", state.ErrNodeNotInNodeStore))

		var em *huma.ErrorModel
		require.ErrorAs(t, err, &em)
		require.Len(t, em.Errors, 1)
		assert.Contains(t, em.Errors[0].Message, "node no longer exists in NodeStore")
	})
}
