package apiv2

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
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
// contract the Tailscale SDK relies on: its IsNotFound() keys off the
// 404 status, so a sentinel mapped to the wrong status turns a
// recoverable "resource gone" into a retryable 500 (or vice versa).
// Every sentinel a v2 handler can actually produce is covered, in
// both bare and wrapped form (state wraps sentinels with %w before
// returning them, e.g. fmt.Errorf("renaming node: %w", ...)).
func TestMapErrorTranslatesSentinels(t *testing.T) {
	t.Parallel()

	// Mirrors the wrap shapes state produces: SetNodeExpiry wraps the
	// sentinel with the node id, then the handler adds its context.
	wrappedNodeStoreMiss := fmt.Errorf("setting device key expiry: %w",
		fmt.Errorf("%w: %d", state.ErrNodeNotInNodeStore, types.NodeID(7)))
	wrappedRenameCollision := fmt.Errorf("renaming device: %w",
		fmt.Errorf("%w: %s", state.ErrNodeNameNotUnique, "dup"))
	wrappedUnrelated := fmt.Errorf("setting device tags: %w", errTestDBDown)

	tests := []struct {
		name string
		err  error
		want int
	}{
		// Not found: the SDK treats these as "resource gone".
		{"gorm record not found", gorm.ErrRecordNotFound, http.StatusNotFound},
		{"preauth key not found", db.ErrPreAuthKeyNotFound, http.StatusNotFound},
		{"user not found", db.ErrUserNotFound, http.StatusNotFound},
		{"node not found", state.ErrNodeNotFound, http.StatusNotFound},
		// The node vanished between the handler's lookup and the state
		// mutation (delete race): the same 404, not a 500.
		{"node left the NodeStore", state.ErrNodeNotInNodeStore, http.StatusNotFound},

		// Invalid input: the caller can fix this request.
		{"preauth key not tagged or owned", db.ErrPreAuthKeyNotTaggedOrOwned, http.StatusBadRequest},
		{"preauth key ACL tag invalid", db.ErrPreAuthKeyACLTagInvalid, http.StatusBadRequest},
		{"given name invalid", state.ErrGivenNameInvalid, http.StatusBadRequest},
		{"given name taken", state.ErrGivenNameTaken, http.StatusBadRequest},
		{"node name not unique", state.ErrNodeNameNotUnique, http.StatusBadRequest},
		{"requested tags invalid", state.ErrRequestedTagsInvalidOrNotPermitted, http.StatusBadRequest},

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

		err := mapError("deleting device",
			fmt.Errorf("deleting device: %w", state.ErrNodeNotInNodeStore))

		var em *huma.ErrorModel
		require.ErrorAs(t, err, &em)
		require.Len(t, em.Errors, 1)
		assert.Contains(t, em.Errors[0].Message, "node no longer exists in NodeStore")
	})
}

// TestTailscaleErrorTransformer pins the v2 error body shape the
// Tailscale Go client, tscli, and the Terraform provider decode:
// {"message","data","status"} — not Huma's RFC 9457 problem+json,
// which would reach those clients with an empty message.
func TestTailscaleErrorTransformer(t *testing.T) {
	t.Parallel()

	t.Run("detail becomes the message", func(t *testing.T) {
		t.Parallel()

		out, err := tailscaleErrorTransformer(nil, "", &huma.ErrorModel{ //nolint:exhaustruct_v5
			Title:  "Not Found",
			Detail: "device not found",
			Status: http.StatusNotFound,
		})
		require.NoError(t, err)
		assert.Equal(t, apiError{ //nolint:exhaustruct_v5
			Message: "device not found",
			Status:  http.StatusNotFound,
		}, out)
	})

	t.Run("title is the fallback message", func(t *testing.T) {
		t.Parallel()

		out, err := tailscaleErrorTransformer(nil, "", &huma.ErrorModel{ //nolint:exhaustruct_v5
			Title:  "Bad Request",
			Status: http.StatusBadRequest,
		})
		require.NoError(t, err)
		assert.Equal(t, apiError{ //nolint:exhaustruct_v5
			Message: "Bad Request",
			Status:  http.StatusBadRequest,
		}, out)
	})

	t.Run("error details are carried in data", func(t *testing.T) {
		t.Parallel()

		out, err := tailscaleErrorTransformer(nil, "", &huma.ErrorModel{ //nolint:exhaustruct_v5
			Title:  "Bad Request",
			Detail: "setting device tags",
			Status: http.StatusBadRequest,
			Errors: []*huma.ErrorDetail{
				{Message: "tag:prod is not defined in policy"}, //nolint:exhaustruct_v5
			},
		})
		require.NoError(t, err)
		assert.Equal(t, apiError{ //nolint:exhaustruct_v5
			Message: "setting device tags",
			Status:  http.StatusBadRequest,
			Data: []apiErrorData{ //nolint:exhaustruct_v5
				{Errors: []string{"tag:prod is not defined in policy"}},
			},
		}, out)
	})

	t.Run("non-error models pass through untouched", func(t *testing.T) {
		t.Parallel()

		out, err := tailscaleErrorTransformer(nil, "", "plain string body")
		require.NoError(t, err)
		assert.Equal(t, "plain string body", out)
	})
}

// TestV2ErrorWireShape is the end-to-end contract: a huma error
// returned by a handler leaves the wire as the Tailscale body, with
// the status code the SDK's IsNotFound keys off. The garbage device
// id fails in parseNodeID before any State access, so a zero Backend
// is safe.
func TestV2ErrorWireShape(t *testing.T) {
	t.Parallel()

	_, api := humatest.New(t, Config())
	Register(api, Backend{}) //nolint:exhaustruct_v5

	resp := api.Get("/api/v2/device/notanumber")
	require.Equal(t, http.StatusNotFound, resp.Code)

	var body apiError
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &body))
	assert.Equal(t, "device not found", body.Message)
	assert.Equal(t, http.StatusNotFound, body.Status)
	assert.Empty(t, body.Data)
}

// TestBearerOrBasicToken pins the credential extraction the auth
// middleware gates on. The Tailscale SDK sends the key as the Basic
// username with an empty password; curl and humans use Bearer. A
// malformed header must fail closed (ok=false), never yield an empty
// token that could match an empty-key account.
func TestBearerOrBasicToken(t *testing.T) {
	t.Parallel()

	basic := func(raw string) string {
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(raw))
	}

	tests := []struct {
		name   string
		header string
		want   string
		wantOK bool
	}{
		{"bearer token", "Bearer hskey-api-x", "hskey-api-x", true},
		{"bearer empty", "Bearer ", "", false},
		{"basic key with empty password", basic("hskey-api-x:"), "hskey-api-x", true},
		{"basic key with password", basic("hskey-api-x:secret"), "hskey-api-x", true},
		{"basic key without colon", basic("hskey-api-x"), "hskey-api-x", true},
		{"basic non-base64", "Basic !!!not-base64!!!", "", false},
		{"basic empty", "Basic ", "", false},
		{"empty header", "", "", false},
		{"unknown scheme", "Digest abc", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := bearerOrBasicToken(tc.header)
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestExpirySeconds pins the pre-auth key lifetime contract: whole
// seconds, rounded, because the stored expiration is stamped a hair
// before created+lifetime (an unrounded readback of an 86400s key
// would be 86399s), and clamped at zero for nil or inverted ranges.
func TestExpirySeconds(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		created *time.Time
		expires *time.Time
		want    int64
	}{
		{"nil created", nil, &created, 0},
		{"nil expires", &created, nil, 0},
		{"both nil", nil, nil, 0},
		{"exact day", &created, new(created.Add(86400 * time.Second)), 86400},
		{"hair-early expiry rounds up", &created,
			new(created.Add(86400*time.Second - time.Millisecond)), 86400},
		{"sub-second rounds down", &created,
			new(created.Add(86399*time.Second + 400*time.Millisecond)), 86399},
		{"inverted range clamps to zero", &created,
			new(created.Add(-time.Hour)), 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, expirySeconds(tc.created, tc.expires))
		})
	}
}

// TestParseIDRejectsGarbageWith404 pins the id-resolution failure
// mode: a non-numeric or overflowing path segment is an unknown
// entity (404 naming the subject), never a 400 or a 500, so the
// SDK's IsNotFound behaves for any malformed id a client sends.
func TestParseIDRejectsGarbageWith404(t *testing.T) {
	t.Parallel()

	id, err := parseID("42", "device")
	require.NoError(t, err)
	assert.Equal(t, uint64(42), id)

	for _, raw := range []string{"abc", "-1", "99999999999999999999999", ""} {
		_, err = parseNodeID(raw)
		require.Errorf(t, err, "id %q", raw)
		assert.Equal(t, http.StatusNotFound, statusOf(t, err))

		var em *huma.ErrorModel
		require.ErrorAs(t, err, &em)
		assert.Equal(t, "device not found", em.Detail)
	}
}

// TestRequireDefaultTailnet pins the single-tailnet contract: the
// Tailscale SDK always sends "-"; any other tailnet name is 404
// ("no such tailnet"), which is how the SDK's IsNotFound signals it.
func TestRequireDefaultTailnet(t *testing.T) {
	t.Parallel()

	require.NoError(t, requireDefaultTailnet("-"))
	assert.Equal(t, http.StatusNotFound, statusOf(t, requireDefaultTailnet("other")))
}

// TestEmptyIfNil pins the empty-list wire shape: Tailscale clients
// decode a JSON null list as nil and misbehave, so empty slices must
// serialize as [].
func TestEmptyIfNil(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{}, emptyIfNil(nil))

	got := []string{"tag:ci"}
	assert.Equal(t, got, emptyIfNil(got))
}
