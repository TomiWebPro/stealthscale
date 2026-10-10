package hscontrol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
	"golang.org/x/oauth2"
)

// pkceTokenRequest captures what the fake IdP token endpoint received.
type pkceTokenRequest struct {
	Code         string
	CodeVerifier string
	GrantType    string
}

// newPKCETestProvider builds an AuthProviderOIDC whose oauth2 config
// targets a test token endpoint that records the exchange form values
// and replies with a valid token. PKCE (S256) is enabled by default.
func newPKCETestProvider(t *testing.T, got *pkceTokenRequest) *AuthProviderOIDC {
	t.Helper()

	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.NoError(t, r.ParseForm())

		mu.Lock()
		*got = pkceTokenRequest{
			Code:         r.PostForm.Get("code"),
			CodeVerifier: r.PostForm.Get("code_verifier"),
			GrantType:    r.PostForm.Get("grant_type"),
		}
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "test-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	t.Cleanup(srv.Close)

	//nolint:exhaustruct_v5 // partial literal is deliberate: only the exchange-relevant fields
	return &AuthProviderOIDC{
		//nolint:exhaustruct_v5 // partial literal is deliberate: only the PKCE-relevant fields
		cfg: &types.OIDCConfig{
			ClientID: "test-client",
			PKCE:     types.PKCEConfig{Enabled: true, Method: types.PKCEMethodS256},
		},
		authCache: expirable.NewLRU[string, AuthInfo](authCacheMaxEntries, nil, authCacheExpiration),
		//nolint:exhaustruct_v5 // partial literal is deliberate: only the token endpoint matters
		oauth2Config: &oauth2.Config{
			ClientID:     "test-client",
			ClientSecret: "test-secret",
			//nolint:exhaustruct_v5 // partial literal is deliberate: only TokenURL is used
			Endpoint: oauth2.Endpoint{TokenURL: srv.URL},
		},
	}
}

// TestGetOauth2TokenPKCESendsVerifier pins the PKCE token exchange:
// the verifier generated at /auth must reach the IdP token endpoint
// as code_verifier. A regression dropping oauth2.VerifierOption would
// downgrade every login to no-PKCE — the IdP rejects the exchange,
// but only the wire shape reveals why, so pin it here.
func TestGetOauth2TokenPKCESendsVerifier(t *testing.T) {
	var got pkceTokenRequest

	a := newPKCETestProvider(t, &got)

	verifier := "test-verifier-value"
	//nolint:exhaustruct_v5 // partial literal is deliberate: only the verifier matters
	a.authCache.Add("state-abc", AuthInfo{Verifier: &verifier})

	tok, err := a.getOauth2Token(context.Background(), "auth-code", "state-abc")
	require.NoError(t, err)
	require.NotNil(t, tok)

	assert.Equal(t, "auth-code", got.Code)
	assert.Equal(t, "authorization_code", got.GrantType)
	assert.Equal(t, verifier, got.CodeVerifier,
		"the cached PKCE verifier must be sent as code_verifier")
}

// TestGetOauth2TokenPKCEMissingState pins the fail-closed path: with
// PKCE enabled, an unknown or expired state is a 404 carrying
// errNoOIDCRegistrationInfo — never a silent verifier-less exchange.
func TestGetOauth2TokenPKCEMissingState(t *testing.T) {
	var got pkceTokenRequest

	a := newPKCETestProvider(t, &got)

	_, err := a.getOauth2Token(context.Background(), "auth-code", "missing-state")
	require.Error(t, err)

	var httpErr HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusNotFound, httpErr.Code)
	require.ErrorIs(t, err, errNoOIDCRegistrationInfo)
}

// TestGetOauth2TokenPKCENilVerifier pins the config-reload edge: a
// cached entry without a verifier (PKCE was off when /auth minted it)
// exchanges without code_verifier rather than erroring — the IdP only
// requires the verifier when a challenge was sent at /auth time.
func TestGetOauth2TokenPKCENilVerifier(t *testing.T) {
	var got pkceTokenRequest

	a := newPKCETestProvider(t, &got)

	//nolint:exhaustruct_v5 // empty entry is the point: no verifier was minted at /auth time
	a.authCache.Add("state-abc", AuthInfo{})

	tok, err := a.getOauth2Token(context.Background(), "auth-code", "state-abc")
	require.NoError(t, err)
	require.NotNil(t, tok)
	assert.Empty(t, got.CodeVerifier)
}

// TestGetOauth2TokenWithoutPKCE pins that the state cache is only
// consulted when PKCE is enabled: with PKCE off the exchange proceeds
// against an empty cache (no spurious 404, no verifier lookup).
func TestGetOauth2TokenWithoutPKCE(t *testing.T) {
	var got pkceTokenRequest

	a := newPKCETestProvider(t, &got)
	a.cfg.PKCE.Enabled = false

	tok, err := a.getOauth2Token(context.Background(), "auth-code", "any-state")
	require.NoError(t, err)
	require.NotNil(t, tok)
	assert.Empty(t, got.CodeVerifier)
}

// newFailingTokenEndpointProvider builds an AuthProviderOIDC
// whose token endpoint replies 500, simulating an IdP hiccup
// (transient outage, bad gateway, malformed reply) during the
// code exchange.
func newFailingTokenEndpointProvider(t *testing.T) *AuthProviderOIDC {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "idp unavailable", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	//nolint:exhaustruct_v5 // partial literal is deliberate: only the exchange-relevant fields
	return &AuthProviderOIDC{
		//nolint:exhaustruct_v5 // partial literal is deliberate: only PKCE-relevant fields
		cfg: &types.OIDCConfig{
			ClientID: "test-client",
			PKCE:     types.PKCEConfig{Enabled: true, Method: types.PKCEMethodS256},
		},
		authCache: expirable.NewLRU[string, AuthInfo](authCacheMaxEntries, nil, authCacheExpiration),
		//nolint:exhaustruct_v5 // partial literal is deliberate: only TokenURL is used
		oauth2Config: &oauth2.Config{
			ClientID:     "test-client",
			ClientSecret: "test-secret",
			//nolint:exhaustruct_v5 // partial literal is deliberate: only TokenURL is used
			Endpoint: oauth2.Endpoint{TokenURL: srv.URL},
		},
	}
}

// TestGetOauth2TokenIdPHiccupFailsClosed pins the IdP-hiccup
// failure mode: when the IdP token endpoint errors (500, network
// blip, non-JSON reply), the exchange must fail closed with a
// 403 HTTPError wrapping the transport error — never a nil-token
// success that would skip ID-token verification, and never a
// panic. The caller (OIDCCallbackHandler) turns the HTTPError
// into a user-facing error so the browser retry starts a fresh
// flow instead of half-authenticating.
func TestGetOauth2TokenIdPHiccupFailsClosed(t *testing.T) {
	a := newFailingTokenEndpointProvider(t)

	verifier := "test-verifier-value"
	//nolint:exhaustruct_v5 // entry with verifier: the exchange must be attempted against the dead endpoint
	a.authCache.Add("state-abc", AuthInfo{Verifier: &verifier})

	tok, err := a.getOauth2Token(context.Background(), "auth-code", "state-abc")
	require.Error(t, err)
	assert.Nil(t, tok, "a failed exchange must not yield a token")

	var httpErr HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusForbidden, httpErr.Code)
	require.ErrorContains(t, httpErr.Err, "exchanging code for token")
}

// TestExtractIDTokenMissingFromTokenResponse pins the missing-
// id_token failure mode: a token reply without an id_token (wrong
// scopes, misconfigured client) is a 400 carrying
// errNoOIDCIDToken — not a nil-deref on the provider and not a
// half-verified login.
func TestExtractIDTokenMissingFromTokenResponse(t *testing.T) {
	var got pkceTokenRequest

	// The provider's oidcProvider is nil; extractIDToken must
	// return before touching it when the token carries no
	// id_token.
	a := newPKCETestProvider(t, &got)

	//nolint:exhaustruct_v5 // partial literal is deliberate: only AccessToken matters, no id_token
	_, err := a.extractIDToken(context.Background(), &oauth2.Token{AccessToken: "at"})
	require.Error(t, err)

	var httpErr HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusBadRequest, httpErr.Code)
	require.ErrorIs(t, err, errNoOIDCIDToken)
}
