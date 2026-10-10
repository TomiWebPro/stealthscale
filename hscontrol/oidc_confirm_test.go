package hscontrol

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
)

func newConfirmRequest(t *testing.T, authID types.AuthID, formCSRF, cookieCSRF string) *http.Request {
	t.Helper()

	form := strings.NewReader(registerConfirmCSRFCookie + "=" + formCSRF)
	req := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"/register/confirm/"+authID.String(),
		form,
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	//nolint:gosec // G124: test fixture
	req.AddCookie(&http.Cookie{
		Name:  registerConfirmCSRFCookie,
		Value: cookieCSRF,
	})

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("auth_id", authID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	return req
}

// TestRegisterConfirmHandler_RejectsCSRFMismatch verifies that the
// /register/confirm POST handler refuses to finalise a pending
// registration when the form CSRF token does not match the cookie.
func TestRegisterConfirmHandler_RejectsCSRFMismatch(t *testing.T) {
	t.Parallel()

	app := createTestApp(t)
	provider := &AuthProviderOIDC{h: app}

	// Mint a pending registration with a stashed pending-confirmation,
	// as the OIDC callback would have done after resolving the user
	// identity but before the user clicked the interstitial form.
	authID := types.MustAuthID()
	regReq := types.NewRegisterAuthRequest(&types.RegistrationData{
		Hostname: "phish-target",
	})
	regReq.SetPendingConfirmation(&types.PendingRegistrationConfirmation{
		UserID: 1,
		CSRF:   "expected-csrf",
	})
	app.state.SetAuthCacheEntry(authID, regReq)

	rec := httptest.NewRecorder()
	provider.RegisterConfirmHandler(rec,
		newConfirmRequest(t, authID, "wrong-csrf", "expected-csrf"),
	)

	assert.Equal(t, http.StatusForbidden, rec.Code,
		"CSRF cookie/form mismatch must be rejected with 403")

	// And the registration must still be pending — the rejected POST
	// must not have called [AuthProviderOIDC.handleRegistration].
	cached, ok := app.state.GetAuthCacheEntry(authID)
	require.True(t, ok, "rejected POST must not evict the cached registration")
	require.NotNil(t, cached.PendingConfirmation(),
		"rejected POST must not clear the pending confirmation")
}

// TestRegisterConfirmHandler_SessionExpired verifies that
// /register/confirm fails closed when the registration session is
// gone by the time the user clicks the interstitial — the
// authCache TTL (15 min), eviction, or a control-plane restart
// between the OIDC callback and the confirm POST. Without the
// cache-lookup guard the POST could finalise a registration for
// an attacker who knows an auth_id after its session lapsed.
func TestRegisterConfirmHandler_SessionExpired(t *testing.T) {
	t.Parallel()

	app := createTestApp(t)
	provider := &AuthProviderOIDC{h: app}

	// An auth_id with no cached registration session: the OIDC
	// callback ran (the interstitial was rendered) but the entry
	// has since expired or been evicted.
	authID := types.MustAuthID()

	rec := httptest.NewRecorder()
	provider.RegisterConfirmHandler(rec,
		newConfirmRequest(t, authID, "any-csrf", "any-csrf"),
	)

	assert.Equal(t, http.StatusGone, rec.Code,
		"an expired/evicted registration session must be rejected with 410")
}

// TestRegisterConfirmHandler_RejectsNonPOST verifies that
// /register/confirm only accepts POST: the confirmation is a
// state-changing operation and must not be reachable via GET,
// where a cross-site link could trigger it without the form
// body the CSRF check reads.
func TestRegisterConfirmHandler_RejectsNonPOST(t *testing.T) {
	t.Parallel()

	app := createTestApp(t)
	provider := &AuthProviderOIDC{h: app}

	authID := types.MustAuthID()

	req := httptest.NewRequestWithContext(
		context.Background(), http.MethodGet,
		"/register/confirm/"+authID.String(), nil,
	)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("auth_id", authID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	provider.RegisterConfirmHandler(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code,
		"a non-POST confirm request must be rejected with 405")
}

// TestRegisterConfirmHandler_RejectsWithoutPending verifies that
// /register/confirm refuses to finalise a registration that did not
// first complete the OIDC interstitial. Without this check an attacker
// who knew an auth_id could POST directly to the confirm endpoint and
// claim the device.
func TestRegisterConfirmHandler_RejectsWithoutPending(t *testing.T) {
	t.Parallel()

	app := createTestApp(t)
	provider := &AuthProviderOIDC{h: app}

	authID := types.MustAuthID()
	// Cached registration with NO pending confirmation set — i.e. the
	// OIDC callback has not run yet.
	app.state.SetAuthCacheEntry(authID, types.NewRegisterAuthRequest(
		&types.RegistrationData{Hostname: "no-oidc-yet"},
	))

	rec := httptest.NewRecorder()
	provider.RegisterConfirmHandler(rec,
		newConfirmRequest(t, authID, "fake", "fake"),
	)

	assert.Equal(t, http.StatusForbidden, rec.Code,
		"confirm without prior OIDC pending state must be rejected with 403")
}
