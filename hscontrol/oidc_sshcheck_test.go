package hscontrol

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
	"golang.org/x/oauth2"
)

// Fixed identity carried by the fake IdP's ID token (see
// newSSHCheckCallbackIdP). The nonce must match the callback
// request's nonce cookie; the subject is what the SSH check
// identity binding compares against the source node's owner.
const (
	//nolint:gosec // G101: test fixture, not a credential
	sshCheckIDTokenNonce = "sshcheck-nonce-0123456789"
	//nolint:gosec // G101: test fixture, not a credential
	sshCheckIDTokenSub = "sshcheck-user-sub"
	sshCheckOIDCState  = "sshcheck-state-0123456789abcdef"
	sshCheckClientID   = "sshcheck-test-client"
)

// signTestIDToken builds a minimal RS256 JWT signed with key,
// carrying claims. It exists so the callback tests can mint a
// verifiable ID token without depending on a JWT library.
func signTestIDToken(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()

	headerJSON, err := json.Marshal(map[string]string{"alg": "RS256", "kid": "test-key", "typ": "JWT"})
	require.NoError(t, err)

	claimsJSON, err := json.Marshal(claims)
	require.NoError(t, err)

	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." +
		base64.RawURLEncoding.EncodeToString(claimsJSON)

	digest := sha256.Sum256([]byte(signingInput))

	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	require.NoError(t, err)

	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// sshCheckIdPConfig carries the fake IdP's failure-injection
// knobs; the zero value serves the happy path.
type sshCheckIdPConfig struct {
	// failTokenEndpoint makes the token endpoint reply 500
	// instead of minting a token: an IdP outage during the
	// authorization-code exchange.
	failTokenEndpoint bool
}

// sshCheckIdPOption customizes the fake IdP served by
// newSSHCheckCallbackIdP.
type sshCheckIdPOption func(*sshCheckIdPConfig)

// failTokenEndpoint makes the fake IdP's token endpoint reply
// 500 (IdP outage during the code exchange) while discovery
// and JWKS keep working, so a real *oidc.Provider can still
// be built against it.
func failTokenEndpoint() sshCheckIdPOption {
	return func(cfg *sshCheckIdPConfig) {
		cfg.failTokenEndpoint = true
	}
}

// newSSHCheckCallbackIdP starts a fake OpenID Connect provider
// whose token endpoint mints one fixed RS256 ID token (subject
// sshCheckIDTokenSub, nonce sshCheckIDTokenNonce). Discovery
// and JWKS are served so a real *oidc.Provider can be built
// against it. The userinfo endpoint is deliberately not served:
// the callback must tolerate a failing userinfo fetch (an OIDC
// hiccup) and proceed on the ID-token claims alone. The second
// return value counts token-endpoint calls, so tests can assert
// the callback fails closed BEFORE exchanging an authorization
// code whose CSRF state did not verify.
func newSSHCheckCallbackIdP(t *testing.T, opts ...sshCheckIdPOption) (string, *atomic.Int64) {
	t.Helper()

	var cfg sshCheckIdPConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	var (
		srv        *httptest.Server
		idToken    string
		tokenCalls atomic.Int64
	)

	mux := http.NewServeMux()

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 srv.URL,
			"authorization_endpoint": srv.URL + "/auth",
			"token_endpoint":         srv.URL + "/token",
			"jwks_uri":               srv.URL + "/jwks",
		})
	})

	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{
				"kty": "RSA",
				"use": "sig",
				"alg": "RS256",
				"kid": "test-key",
				"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
				"e":   "AQAB",
			}},
		})
	})

	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		tokenCalls.Add(1)

		if cfg.failTokenEndpoint {
			http.Error(w, "idp unavailable", http.StatusInternalServerError)

			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "sshcheck-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
			"id_token":     idToken,
		})
	})

	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	now := time.Now()

	idToken = signTestIDToken(t, key, map[string]any{
		"iss":                srv.URL,
		"aud":                sshCheckClientID,
		"sub":                sshCheckIDTokenSub,
		"nonce":              sshCheckIDTokenNonce,
		"email":              "sshcheck-user@example.com",
		"email_verified":     true,
		"preferred_username": "sshcheckuser",
		"name":               "SSH Check User",
		"iat":                now.Unix(),
		"exp":                now.Add(5 * time.Minute).Unix(),
	})

	return srv.URL, &tokenCalls
}

// newSSHCheckOIDCProvider builds an AuthProviderOIDC wired to
// the fake IdP at issuer and the given app, ready to serve
// [AuthProviderOIDC.OIDCCallbackHandler].
func newSSHCheckOIDCProvider(t *testing.T, app *StealthScale, issuer string) *AuthProviderOIDC {
	t.Helper()

	oidcProvider, err := oidc.NewProvider(context.Background(), issuer)
	require.NoError(t, err)

	//nolint:exhaustruct_v5 // partial literal is deliberate: only the callback-relevant fields
	return &AuthProviderOIDC{
		h:         app,
		cfg:       &types.OIDCConfig{ClientID: sshCheckClientID},
		authCache: expirable.NewLRU[string, AuthInfo](authCacheMaxEntries, nil, authCacheExpiration),
		//nolint:exhaustruct_v5,gosec // partial literal is deliberate; G101: test-fixture secret
		oauth2Config: &oauth2.Config{
			ClientID:     sshCheckClientID,
			ClientSecret: "sshcheck-test-secret",
			//nolint:exhaustruct_v5 // partial literal is deliberate: only TokenURL is used
			Endpoint: oauth2.Endpoint{TokenURL: issuer + "/token"},
		},
		oidcProvider: oidcProvider,
	}
}

// sshCheckCallbackRequest builds the IdP redirect back to
// /oidc/callback: the authorization code and state as query
// parameters, plus the CSRF state and nonce cookies the
// callback verifies.
func sshCheckCallbackRequest() *http.Request {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/oidc/callback?code=sshcheck-code&state="+sshCheckOIDCState, nil)

	//nolint:gosec,exhaustruct_v5 // G124: test-fixture cookie
	req.AddCookie(&http.Cookie{
		Name:  getCookieName("state", sshCheckOIDCState),
		Value: sshCheckOIDCState,
	})

	//nolint:gosec,exhaustruct_v5 // G124: test-fixture cookie
	req.AddCookie(&http.Cookie{
		Name:  getCookieName("nonce", sshCheckIDTokenNonce),
		Value: sshCheckIDTokenNonce,
	})

	return req
}

// sshCheckOIDCUser pre-creates the user the fake IdP's ID
// token resolves to, with the matching provider identifier,
// so the callback finds it via GetUserByOIDCIdentifier
// instead of creating a new user mid-test.
func sshCheckOIDCUser(t *testing.T, app *StealthScale, issuer string) *types.User {
	t.Helper()

	user := app.state.CreateUserForTest("sshcheck-owner")

	identifier := (&types.OIDCClaims{Iss: issuer, Sub: sshCheckIDTokenSub}).Identifier() //nolint:exhaustruct_v5 // partial literal: only Iss and Sub feed Identifier()

	_, _, err := app.state.UpdateUser(types.UserID(user.ID), func(u *types.User) error {
		u.ProviderIdentifier = sql.NullString{String: identifier, Valid: true}

		return nil
	})
	require.NoError(t, err)

	return user
}

// TestOIDCCallbackSSHCheckIdentityBinding drives the full OIDC
// callback (code exchange, ID-token verification, nonce check,
// user resolution) into the SSH check-mode branch of
// [AuthProviderOIDC.OIDCCallbackHandler] — the branch that
// records the verdict a waiting SSH check is parked on. That
// branch is the anti-stolen-key control: only the OIDC
// identity that owns the source node may approve its check.
// The test pins every guard: an owner match records an
// accepting verdict, while a foreign owner, a tagged source
// node, a deleted source node, an expired auth session and a
// non-SSH-check session are all refused without delivering
// any verdict.
func TestOIDCCallbackSSHCheckIdentityBinding(t *testing.T) {
	t.Parallel()

	issuer, _ := newSSHCheckCallbackIdP(t)

	tests := []struct {
		name     string
		bindAuth func(t *testing.T, app *StealthScale, authID types.AuthID) *types.AuthRequest
		wantCode int
		// wantVerdict reports whether the handler must deliver
		// an accepting verdict on the auth request's channel.
		wantVerdict bool
	}{
		{
			name: "owner approves own node",
			bindAuth: func(t *testing.T, app *StealthScale, authID types.AuthID) *types.AuthRequest {
				t.Helper()

				user := sshCheckOIDCUser(t, app, issuer)

				src := app.state.CreateNodeForTest(user, "sshcheck-src")
				dst := app.state.CreateNodeForTest(user, "sshcheck-dst")
				app.state.PutNodeInStoreForTest(*src)
				app.state.PutNodeInStoreForTest(*dst)

				authReq := types.NewSSHCheckAuthRequest(src.ID, dst.ID)
				app.state.SetAuthCacheEntry(authID, authReq)

				return authReq
			},
			wantCode:    http.StatusOK,
			wantVerdict: true,
		},
		{
			name: "node owned by another user is refused",
			bindAuth: func(t *testing.T, app *StealthScale, authID types.AuthID) *types.AuthRequest {
				t.Helper()

				other := app.state.CreateUserForTest("sshcheck-other")

				src := app.state.CreateNodeForTest(other, "foreign-src")
				dst := app.state.CreateNodeForTest(other, "foreign-dst")
				app.state.PutNodeInStoreForTest(*src)
				app.state.PutNodeInStoreForTest(*dst)

				authReq := types.NewSSHCheckAuthRequest(src.ID, dst.ID)
				app.state.SetAuthCacheEntry(authID, authReq)

				return authReq
			},
			wantCode:    http.StatusForbidden,
			wantVerdict: false,
		},
		{
			name: "tagged source node is refused",
			bindAuth: func(t *testing.T, app *StealthScale, authID types.AuthID) *types.AuthRequest {
				t.Helper()

				owner := app.state.CreateUserForTest("sshcheck-tagger")

				src := app.state.CreateNodeForTest(owner, "tagged-src")
				tagged := *src
				tagged.Tags = []string{"tag:server"}
				tagged.UserID = nil
				tagged.User = nil
				view := app.state.PutNodeInStoreForTest(tagged)

				authReq := types.NewSSHCheckAuthRequest(view.ID(), src.ID)
				app.state.SetAuthCacheEntry(authID, authReq)

				return authReq
			},
			wantCode:    http.StatusForbidden,
			wantVerdict: false,
		},
		{
			name: "deleted source node is refused",
			bindAuth: func(t *testing.T, app *StealthScale, authID types.AuthID) *types.AuthRequest {
				t.Helper()

				authReq := types.NewSSHCheckAuthRequest(types.NodeID(999999), types.NodeID(999998))
				app.state.SetAuthCacheEntry(authID, authReq)

				return authReq
			},
			wantCode:    http.StatusGone,
			wantVerdict: false,
		},
		{
			name: "expired auth session is refused",
			bindAuth: func(t *testing.T, app *StealthScale, authID types.AuthID) *types.AuthRequest {
				t.Helper()

				// The auth cache entry was evicted before the
				// IdP redirected back: nothing is bound to
				// this auth ID.
				return types.NewSSHCheckAuthRequest(types.NodeID(1), types.NodeID(2))
			},
			wantCode:    http.StatusGone,
			wantVerdict: false,
		},
		{
			name: "non-SSH-check session is refused",
			bindAuth: func(t *testing.T, app *StealthScale, authID types.AuthID) *types.AuthRequest {
				t.Helper()

				user := app.state.CreateUserForTest("sshcheck-plain")

				src := app.state.CreateNodeForTest(user, "plain-src")
				app.state.PutNodeInStoreForTest(*src)

				authReq := types.NewAuthRequest()
				app.state.SetAuthCacheEntry(authID, authReq)

				return authReq
			},
			wantCode:    http.StatusBadRequest,
			wantVerdict: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			app := createTestApp(t)
			provider := newSSHCheckOIDCProvider(t, app, issuer)

			authID := types.MustAuthID()
			provider.authCache.Add(sshCheckOIDCState, AuthInfo{AuthID: authID}) //nolint:exhaustruct_v5 // partial literal: only AuthID is set

			authReq := tc.bindAuth(t, app, authID)

			rec := httptest.NewRecorder()
			provider.OIDCCallbackHandler(rec, sshCheckCallbackRequest())

			assert.Equal(t, tc.wantCode, rec.Code)

			if tc.wantVerdict {
				select {
				case verdict := <-authReq.WaitForAuth():
					assert.True(t, verdict.Accept(),
						"an approved SSH check must deliver an accepting verdict")
				default:
					t.Fatal("the approved SSH check must deliver a verdict")
				}

				return
			}

			assert.Empty(t, authReq.WaitForAuth(),
				"a refused SSH check must not deliver any verdict")
		})
	}
}

// TestOIDCCallbackCSRFStateNonceGuards pins the callback's own
// CSRF verification — the state and nonce checks that gate the
// authorization-code exchange and every verdict the callback
// can record. A callback whose query state does not match the
// browser's state cookie (or whose ID-token nonce does not match
// the nonce cookie) is a cross-site forgery: it must fail closed
// with 403/400, and a state that does not verify must never
// reach the IdP's token endpoint — the code is only exchangeable
// once the CSRF state has been verified against the cookie.
func TestOIDCCallbackCSRFStateNonceGuards(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		cookies  []http.Cookie
		wantCode int
		// wantExchange reports whether the (verified-state) flow
		// must reach the IdP's token endpoint before failing.
		wantExchange bool
	}{
		{
			name: "state cookie value differs from the query state",
			cookies: []http.Cookie{
				//nolint:gosec,exhaustruct_v5 // G124: test-fixture cookie
				{Name: getCookieName("state", sshCheckOIDCState), Value: "tampered-state-differs-from-query"},
				//nolint:gosec,exhaustruct_v5 // G124: test-fixture cookie
				{Name: getCookieName("nonce", sshCheckIDTokenNonce), Value: sshCheckIDTokenNonce},
			},
			wantCode:     http.StatusForbidden,
			wantExchange: false,
		},
		{
			name: "state cookie missing",
			cookies: []http.Cookie{
				//nolint:gosec,exhaustruct_v5 // G124: test-fixture cookie
				{Name: getCookieName("nonce", sshCheckIDTokenNonce), Value: sshCheckIDTokenNonce},
			},
			wantCode:     http.StatusBadRequest,
			wantExchange: false,
		},
		{
			name: "nonce cookie missing",
			cookies: []http.Cookie{
				//nolint:gosec,exhaustruct_v5 // G124: test-fixture cookie
				{Name: getCookieName("state", sshCheckOIDCState), Value: sshCheckOIDCState},
			},
			wantCode:     http.StatusBadRequest,
			wantExchange: true,
		},
		{
			name: "nonce cookie value differs from the ID token nonce",
			cookies: []http.Cookie{
				//nolint:gosec,exhaustruct_v5 // G124: test-fixture cookie
				{Name: getCookieName("state", sshCheckOIDCState), Value: sshCheckOIDCState},
				//nolint:gosec,exhaustruct_v5 // G124: test-fixture cookie
				{Name: getCookieName("nonce", sshCheckIDTokenNonce), Value: "tampered-nonce"},
			},
			wantCode:     http.StatusForbidden,
			wantExchange: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// A fresh fake IdP per case so the token-endpoint
			// call count is exact for each parallel subtest.
			issuer, tokenCalls := newSSHCheckCallbackIdP(t)

			app := createTestApp(t)
			provider := newSSHCheckOIDCProvider(t, app, issuer)

			// Bind the state to an auth session the way the
			// /auth handler would have, so only the CSRF guards
			// can stop this callback.
			provider.authCache.Add(sshCheckOIDCState, AuthInfo{AuthID: types.MustAuthID()}) //nolint:exhaustruct_v5 // partial literal: only AuthID is set

			// A bare callback request: only the case's cookies
			// ride along, so a tampered cookie is the one the
			// handler finds (http.Request.Cookie returns the
			// first name match — a fixture cookie would shadow
			// the tampered one and mask the guard).
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
				"/oidc/callback?code=sshcheck-code&state="+sshCheckOIDCState, nil)

			for _, c := range tc.cookies {
				cookie := c
				//nolint:gosec // G124: test-fixture cookie
				req.AddCookie(&cookie)
			}

			rec := httptest.NewRecorder()
			provider.OIDCCallbackHandler(rec, req)

			assert.Equal(t, tc.wantCode, rec.Code)

			calls := tokenCalls.Load()
			if tc.wantExchange {
				assert.Equal(t, int64(1), calls,
					"a verified state must reach the token endpoint exactly once before the nonce check")
			} else {
				assert.Zero(t, calls,
					"an unverified state must fail closed before the authorization code is exchanged")
			}
		})
	}
}

// TestOIDCCallbackIdPHiccupFailsClosed drives the full
// callback against an IdP whose token endpoint is down (500):
// discovery and JWKS still answer, so only the exchange can
// fail. The exchange unit itself is pinned by
// TestGetOauth2TokenIdPHiccupFailsClosed; this test pins the
// integration through OIDCCallbackHandler — an IdP outage must
// surface as a user-facing 403, and, critically, must never
// deliver an SSH check verdict: a parked `tailscale ssh`
// session stays parked (fail-closed) instead of being silently
// approved by a half-completed login. The bound state and the
// owner-matched nodes below would approve the check if the
// flow completed, so only the IdP outage can stop the callback.
func TestOIDCCallbackIdPHiccupFailsClosed(t *testing.T) {
	t.Parallel()

	issuer, tokenCalls := newSSHCheckCallbackIdP(t, failTokenEndpoint())

	app := createTestApp(t)
	provider := newSSHCheckOIDCProvider(t, app, issuer)

	authID := types.MustAuthID()
	provider.authCache.Add(sshCheckOIDCState, AuthInfo{AuthID: authID}) //nolint:exhaustruct_v5 // partial literal: only AuthID is set

	user := sshCheckOIDCUser(t, app, issuer)

	src := app.state.CreateNodeForTest(user, "sshcheck-src")
	dst := app.state.CreateNodeForTest(user, "sshcheck-dst")
	app.state.PutNodeInStoreForTest(*src)
	app.state.PutNodeInStoreForTest(*dst)

	authReq := types.NewSSHCheckAuthRequest(src.ID, dst.ID)
	app.state.SetAuthCacheEntry(authID, authReq)

	rec := httptest.NewRecorder()
	provider.OIDCCallbackHandler(rec, sshCheckCallbackRequest())

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Empty(t, authReq.WaitForAuth(),
		"a failed token exchange must not deliver any SSH check verdict")
	assert.Positive(t, tokenCalls.Load(),
		"the callback must have attempted the exchange (the oauth2 library may probe the token endpoint twice, retrying the credentials in the other auth style, before giving up)")
}
