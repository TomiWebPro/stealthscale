package hscontrol

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
	"golang.org/x/oauth2"
)

func TestDoOIDCAuthorization(t *testing.T) {
	testCases := []struct {
		name    string
		cfg     *types.OIDCConfig
		claims  *types.OIDCClaims
		wantErr bool
	}{
		{
			name:    "verified email domain",
			wantErr: false,
			cfg: &types.OIDCConfig{
				EmailVerifiedRequired: true,
				AllowedDomains:        []string{"test.com"},
				AllowedUsers:          []string{},
				AllowedGroups:         []string{},
			},
			claims: &types.OIDCClaims{
				Email:         "user@test.com",
				EmailVerified: true,
			},
		},
		{
			name:    "verified email user",
			wantErr: false,
			cfg: &types.OIDCConfig{
				EmailVerifiedRequired: true,
				AllowedDomains:        []string{},
				AllowedUsers:          []string{"user@test.com"},
				AllowedGroups:         []string{},
			},
			claims: &types.OIDCClaims{
				Email:         "user@test.com",
				EmailVerified: true,
			},
		},
		{
			name:    "unverified email domain",
			wantErr: true,
			cfg: &types.OIDCConfig{
				EmailVerifiedRequired: true,
				AllowedDomains:        []string{"test.com"},
				AllowedUsers:          []string{},
				AllowedGroups:         []string{},
			},
			claims: &types.OIDCClaims{
				Email:         "user@test.com",
				EmailVerified: false,
			},
		},
		{
			name:    "group member",
			wantErr: false,
			cfg: &types.OIDCConfig{
				EmailVerifiedRequired: true,
				AllowedDomains:        []string{},
				AllowedUsers:          []string{},
				AllowedGroups:         []string{"test"},
			},
			claims: &types.OIDCClaims{Groups: []string{"test"}},
		},
		{
			name:    "non group member",
			wantErr: true,
			cfg: &types.OIDCConfig{
				EmailVerifiedRequired: true,
				AllowedDomains:        []string{},
				AllowedUsers:          []string{},
				AllowedGroups:         []string{"nope"},
			},
			claims: &types.OIDCClaims{Groups: []string{"testo"}},
		},
		{
			name:    "group member but bad domain",
			wantErr: true,
			cfg: &types.OIDCConfig{
				EmailVerifiedRequired: true,
				AllowedDomains:        []string{"user@good.com"},
				AllowedUsers:          []string{},
				AllowedGroups:         []string{"test group"},
			},
			claims: &types.OIDCClaims{Groups: []string{"test group"}, Email: "bad@bad.com", EmailVerified: true},
		},
		{
			name:    "all checks pass",
			wantErr: false,
			cfg: &types.OIDCConfig{
				EmailVerifiedRequired: true,
				AllowedDomains:        []string{"test.com"},
				AllowedUsers:          []string{"user@test.com"},
				AllowedGroups:         []string{"test group"},
			},
			claims: &types.OIDCClaims{Groups: []string{"test group"}, Email: "user@test.com", EmailVerified: true},
		},
		{
			name:    "all checks pass with unverified email",
			wantErr: false,
			cfg: &types.OIDCConfig{
				EmailVerifiedRequired: false,
				AllowedDomains:        []string{"test.com"},
				AllowedUsers:          []string{"user@test.com"},
				AllowedGroups:         []string{"test group"},
			},
			claims: &types.OIDCClaims{Groups: []string{"test group"}, Email: "user@test.com", EmailVerified: false},
		},
		{
			name:    "fail on unverified email",
			wantErr: true,
			cfg: &types.OIDCConfig{
				EmailVerifiedRequired: true,
				AllowedDomains:        []string{"test.com"},
				AllowedUsers:          []string{"user@test.com"},
				AllowedGroups:         []string{"test group"},
			},
			claims: &types.OIDCClaims{Groups: []string{"test group"}, Email: "user@test.com", EmailVerified: false},
		},
		{
			name:    "unverified email user only",
			wantErr: true,
			cfg: &types.OIDCConfig{
				EmailVerifiedRequired: true,
				AllowedDomains:        []string{},
				AllowedUsers:          []string{"user@test.com"},
				AllowedGroups:         []string{},
			},
			claims: &types.OIDCClaims{
				Email:         "user@test.com",
				EmailVerified: false,
			},
		},
		{
			name:    "no filters configured",
			wantErr: false,
			cfg: &types.OIDCConfig{
				EmailVerifiedRequired: true,
				AllowedDomains:        []string{},
				AllowedUsers:          []string{},
				AllowedGroups:         []string{},
			},
			claims: &types.OIDCClaims{
				Email:         "anyone@anywhere.com",
				EmailVerified: false,
			},
		},
		{
			name:    "multiple allowed groups second matches",
			wantErr: false,
			cfg: &types.OIDCConfig{
				EmailVerifiedRequired: true,
				AllowedDomains:        []string{},
				AllowedUsers:          []string{},
				AllowedGroups:         []string{"group1", "group2", "group3"},
			},
			claims: &types.OIDCClaims{Groups: []string{"group2"}},
		},
	}

	for _, tC := range testCases {
		t.Run(tC.name, func(t *testing.T) {
			err := doOIDCAuthorization(tC.cfg, tC.claims)
			if ((err != nil) && !tC.wantErr) || ((err == nil) && tC.wantErr) {
				t.Errorf("bad authorization: %s > want=%v | got=%v", tC.name, tC.wantErr, err)
			}
		})
	}
}

// TestSetCSRFCookieSameSite verifies the OIDC state/nonce CSRF cookies carry an
// explicit SameSite=Lax attribute. Lax (not Strict) is required because the
// OIDC callback is a cross-site top-level GET navigation from the IdP that must
// still carry the cookie — Strict would drop it and break login. The cookie
// previously set no SameSite (despite a comment claiming it did), leaving
// browsers that do not default to Lax sending it on cross-site requests.
func TestSetCSRFCookieSameSite(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/abcdef0123456789", nil)

	setCSRFCookie(w, r, "state", false)

	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, http.SameSiteLaxMode, cookies[0].SameSite,
		"OIDC CSRF cookie must explicitly set SameSite=Lax")
}

// TestExtractCodeAndStateParam covers the callback's first trust-boundary
// checks: both params required, and a too-short state is rejected before
// getCookieName can slice out of range.
func TestExtractCodeAndStateParam(t *testing.T) {
	_, _, err := extractCodeAndStateParamFromRequest(
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/oidc/callback", nil))
	require.Error(t, err)

	_, _, err = extractCodeAndStateParamFromRequest(
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/oidc/callback?code=c&state=abc", nil))
	require.ErrorIs(t, err, errOIDCStateTooShort)

	code, state, err := extractCodeAndStateParamFromRequest(
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/oidc/callback?code=c&state=abcdef0123", nil))
	require.NoError(t, err)
	assert.Equal(t, "c", code)
	assert.Equal(t, "abcdef0123", state)
}

// TestGetAuthInfoFromStateSingleUse asserts a consumed OIDC state cannot be
// resolved twice, so a replayed callback cannot re-bind the same session.
func TestGetAuthInfoFromStateSingleUse(t *testing.T) {
	a := &AuthProviderOIDC{
		authCache: expirable.NewLRU[string, AuthInfo](16, nil, time.Minute),
	}
	a.authCache.Add("state-x", AuthInfo{Registration: true})

	got := a.getAuthInfoFromState("state-x")
	require.NotNil(t, got)
	assert.True(t, got.Registration)

	assert.Nil(t, a.getAuthInfoFromState("state-x"), "a consumed state must not resolve again")
}

// TestClearOIDCCallbackCookie asserts the cookie is expired (negative MaxAge) on
// the same path it was set with, so the browser drops it.
func TestClearOIDCCallbackCookie(t *testing.T) {
	w := httptest.NewRecorder()
	clearOIDCCallbackCookie(w, "state_abcdef")

	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, "state_abcdef", cookies[0].Name)
	assert.Negative(t, cookies[0].MaxAge, "deletion cookie must have negative MaxAge")
}

// TestSetCSRFCookieSecure verifies the Secure flag is driven by the secure
// argument (derived from the configured https server_url), not only req.TLS, so
// cookies stay Secure behind a TLS-terminating reverse proxy where req.TLS is
// nil.
func TestSetCSRFCookieSecure(t *testing.T) {
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/abcdef0123456789", nil)

	secureRec := httptest.NewRecorder()
	setCSRFCookie(secureRec, r, "state", true)
	require.Len(t, secureRec.Result().Cookies(), 1)
	assert.True(t, secureRec.Result().Cookies()[0].Secure,
		"https server_url must set Secure even when req.TLS is nil (proxy case)")

	plainRec := httptest.NewRecorder()
	setCSRFCookie(plainRec, r, "state", false)
	require.Len(t, plainRec.Result().Cookies(), 1)
	assert.False(t, plainRec.Result().Cookies()[0].Secure,
		"plain-http server_url without req.TLS must not set Secure")
}

// oidcTestClaims builds fully-populated OIDCClaims so the
// validators under test can be exercised without depending on
// which optional claim fields matter.
func oidcTestClaims(
	email string,
	emailVerified bool,
	groups []string,
) *types.OIDCClaims {
	return &types.OIDCClaims{
		Sub:               "test-sub",
		Iss:               "test-issuer",
		Name:              "Test User",
		Groups:            types.FlexibleStringSlice(groups),
		Email:             email,
		EmailVerified:     types.FlexibleBoolean(emailVerified),
		ProfilePictureURL: "",
		Username:          "testuser",
	}
}

// assertUnauthorized asserts err is an HTTPError carrying 401,
// the status every allow-list rejection must surface as.
func assertUnauthorized(t *testing.T, err error) {
	t.Helper()

	require.Error(t, err)

	var httpErr HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusUnauthorized, httpErr.Code)
}

// TestValidateOIDCAccessors pins the allow-list validators' failure
// points: an unconfigured (empty) list must pass — matching the
// documented contract and validateOIDCAllowedDomains — while a
// configured list rejects the wrong domain/group/user with HTTP 401.
// Regression: validateOIDCAllowedGroups/Users used to return
// "unauthorised" even for an empty list, which would fail every
// login for any future caller that skips the doOIDCAuthorization
// guard.
func TestValidateOIDCAccessors(t *testing.T) {
	t.Run("domains", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			allowed []string
			email   string
			wantErr bool
		}{
			{
				name:    "unconfigured list passes",
				allowed: nil,
				email:   "anyone@example.net",
				wantErr: false,
			},
			{
				name:    "matching domain passes",
				allowed: []string{"test.com"},
				email:   "user@test.com",
				wantErr: false,
			},
			{
				name:    "case-insensitive domain part is not folded",
				allowed: []string{"test.com"},
				email:   "user@TEST.com",
				wantErr: true,
			},
			{
				name:    "other domain rejected",
				allowed: []string{"test.com"},
				email:   "user@evil.example",
				wantErr: true,
			},
			{
				name:    "subdomain not implicitly allowed",
				allowed: []string{"test.com"},
				email:   "user@sub.test.com",
				wantErr: true,
			},
			{
				name:    "email without @ rejected",
				allowed: []string{"test.com"},
				email:   "not-an-email",
				wantErr: true,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				err := validateOIDCAllowedDomains(
					tc.allowed, oidcTestClaims(tc.email, true, nil))

				if tc.wantErr {
					assertUnauthorized(t, err)
					return
				}

				require.NoError(t, err)
			})
		}
	})

	t.Run("groups", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			allowed []string
			groups  []string
			wantErr bool
		}{
			{
				name:    "unconfigured list passes",
				allowed: nil,
				groups:  []string{"anything"},
				wantErr: false,
			},
			{
				name:    "member passes",
				allowed: []string{"admins", "devs"},
				groups:  []string{"users", "devs"},
				wantErr: false,
			},
			{
				name:    "non-member rejected",
				allowed: []string{"admins"},
				groups:  []string{"users"},
				wantErr: true,
			},
			{
				name:    "no groups at all rejected",
				allowed: []string{"admins"},
				groups:  nil,
				wantErr: true,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				err := validateOIDCAllowedGroups(
					tc.allowed, oidcTestClaims("user@test.com", true, tc.groups))

				if tc.wantErr {
					assertUnauthorized(t, err)
					return
				}

				require.NoError(t, err)
			})
		}
	})

	t.Run("users", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			allowed []string
			email   string
			wantErr bool
		}{
			{
				name:    "unconfigured list passes",
				allowed: nil,
				email:   "anyone@example.net",
				wantErr: false,
			},
			{
				name:    "listed user passes",
				allowed: []string{"user@test.com"},
				email:   "user@test.com",
				wantErr: false,
			},
			{
				name:    "unlisted user rejected",
				allowed: []string{"user@test.com"},
				email:   "other@test.com",
				wantErr: true,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				err := validateOIDCAllowedUsers(
					tc.allowed, oidcTestClaims(tc.email, true, nil))

				if tc.wantErr {
					assertUnauthorized(t, err)
					return
				}

				require.NoError(t, err)
			})
		}
	})
}

// TestDetermineNodeExpiry covers the OIDC token-expiry → node-expiry
// mapping: with UseExpiryFromToken the node expires when the ID token
// does (re-auth required after token expiry); without it the node has
// no expiry. Failure mode: a misconfigured flag silently pins nodes
// forever or expires them at token TTL.
func TestDetermineNodeExpiry(t *testing.T) {
	tokenExpiry := time.Date(2026, 10, 10, 5, 0, 0, 0, time.UTC)

	a := &AuthProviderOIDC{
		h:            nil,
		serverURL:    "",
		cfg:          oidcTestConfig(true),
		authCache:    nil,
		oidcProvider: nil,
		oauth2Config: nil,
	}

	got := a.determineNodeExpiry(tokenExpiry)
	require.NotNil(t, got,
		"UseExpiryFromToken must propagate the ID token expiry to the node")
	assert.Equal(t, tokenExpiry, *got)

	a.cfg.UseExpiryFromToken = false
	assert.Nil(t, a.determineNodeExpiry(tokenExpiry),
		"without UseExpiryFromToken the node must not inherit the token expiry")
}

// oidcTestConfig builds a fully-populated OIDCConfig with only the
// authorization-relevant fields set to non-zero values.
func oidcTestConfig(useExpiryFromToken bool) *types.OIDCConfig {
	return &types.OIDCConfig{
		OnlyStartIfOIDCIsAvailable: false,
		Issuer:                     "https://oidc.example.test",
		ClientID:                   "test-client",
		ClientSecret:               "",
		Scope:                      nil,
		ExtraParams:                nil,
		AllowedDomains:             nil,
		AllowedUsers:               nil,
		AllowedGroups:              nil,
		EmailVerifiedRequired:      false,
		UseExpiryFromToken:         useExpiryFromToken,
		PKCE:                       types.PKCEConfig{Enabled: false, Method: ""},
	}
}

// TestDoOIDCAuthorizationUnverifiedEmailPassesWithoutEmailTests
// pins the interaction between EmailVerifiedRequired and the email
// allow-lists: an unverified email only fails when a domain/user
// allow-list is configured; with no email tests configured there is
// nothing to gate and authorization succeeds.
func TestDoOIDCAuthorizationUnverifiedEmailPassesWithoutEmailTests(t *testing.T) {
	cfg := oidcTestConfig(false)
	cfg.EmailVerifiedRequired = true

	claims := oidcTestClaims("user@test.com", false, nil)

	require.NoError(t, doOIDCAuthorization(cfg, claims))

	cfg.AllowedDomains = []string{"test.com"}
	err := doOIDCAuthorization(cfg, claims)

	require.Error(t, err,
		"unverified email must fail once a domain allow-list is configured")

	var httpErr HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusUnauthorized, httpErr.Code)
}

// TestOIDCCallbackStateCSRFRejection pins the /oidc/callback
// CSRF boundary — the first trust check in OIDCCallbackHandler.
// A forged callback, or a real one whose state cookie was lost
// (a stripping proxy, a browser clearing cookies mid-flow), must
// be rejected before the authorization code is exchanged with the
// IdP. The fake token endpoint counts requests so the test can
// prove the exchange is never attempted for a missing or
// mismatched state cookie, while a matching state passes the
// boundary and reaches the exchange (which fails against the
// fake endpoint — proving the flow proceeded past the check).
// Regression guard: reordering the handler to exchange the code
// before validating the state cookie would let a forged callback
// hit the IdP (and consume a code) before being rejected.
func TestOIDCCallbackStateCSRFRejection(t *testing.T) {
	var tokenRequests atomic.Int64

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		tokenRequests.Add(1)
		http.Error(w, "no tokens", http.StatusBadRequest)
	}))
	t.Cleanup(tokenServer.Close)

	newProvider := func() *AuthProviderOIDC {
		return &AuthProviderOIDC{
			h:            nil,
			serverURL:    "",
			cfg:          oidcTestConfig(false),
			authCache:    expirable.NewLRU[string, AuthInfo](authCacheMaxEntries, nil, authCacheExpiration),
			oidcProvider: nil,
			oauth2Config: &oauth2.Config{ //nolint:exhaustruct_v5 // test fixture: only the token endpoint matters
				ClientID:     "test-client",
				ClientSecret: "test-secret",
				Endpoint:     oauth2.Endpoint{TokenURL: tokenServer.URL}, //nolint:exhaustruct_v5 // test fixture: only TokenURL matters
			},
		}
	}

	// The state must be at least cookieNamePrefixLen characters
	// so getCookieName does not truncate; a fixed value keeps the
	// cookie name deterministic.
	const state = "0123456789abcdef0123456789abcdef"

	callbackRequest := func(cookieValue string) *http.Request {
		req := httptest.NewRequestWithContext(
			t.Context(), http.MethodGet,
			"/oidc/callback?code=test-code&state="+state, nil)
		if cookieValue != "" {
			req.AddCookie(&http.Cookie{ //nolint:gosec,exhaustruct_v5 // test fixture cookie; never leaves the test process
				Name:     getCookieName("state", state),
				Value:    cookieValue,
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
			})
		}

		return req
	}

	t.Run("missing state cookie", func(t *testing.T) {
		a := newProvider()
		w := httptest.NewRecorder()
		a.OIDCCallbackHandler(w, callbackRequest(""))

		assert.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
		assert.Zero(t, tokenRequests.Load(),
			"a missing state cookie must be rejected before the token exchange")
	})

	t.Run("state cookie mismatch", func(t *testing.T) {
		a := newProvider()
		w := httptest.NewRecorder()
		a.OIDCCallbackHandler(w, callbackRequest("ffffffffffffffffffffffffffffffff"))

		assert.Equal(t, http.StatusForbidden, w.Result().StatusCode)
		assert.Zero(t, tokenRequests.Load(),
			"a mismatched state cookie must be rejected before the token exchange")
	})

	t.Run("matching state reaches the token exchange", func(t *testing.T) {
		a := newProvider()
		w := httptest.NewRecorder()
		a.OIDCCallbackHandler(w, callbackRequest(state))

		// The exchange runs and fails against the fake endpoint,
		// surfacing as a 403 "invalid code" — proving the request
		// passed the CSRF boundary. The request count is asserted
		// positive (not exact) because the oauth2 library probes
		// auth-style and may retry the exchange.
		assert.Equal(t, http.StatusForbidden, w.Result().StatusCode)
		assert.Positive(t, tokenRequests.Load(),
			"a matching state cookie must proceed to the token exchange")
	})
}
