package apiv2

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
)

// TestNarrowTagsRejectsMalformed asserts the token endpoint validates tag
// format at the trust boundary. An "all"-scope client may assign any tag, but a
// malformed tag (missing the "tag:" prefix) must still be rejected rather than
// flowing unvalidated into auth-key creation.
func TestNarrowTagsRejectsMalformed(t *testing.T) {
	all := &types.OAuthClient{Scopes: []string{"all"}}

	_, bad, ok := narrowTags(all, []string{"not-a-tag"})
	assert.False(t, ok, "malformed tag must be rejected even with all scope")
	assert.Equal(t, "not-a-tag", bad)

	got, _, ok := narrowTags(all, []string{"tag:anything"})
	assert.True(t, ok, "all scope may assign any well-formed tag")
	assert.Equal(t, []string{"tag:anything"}, got)

	// A scoped client may only assign its own well-formed tags.
	scoped := &types.OAuthClient{Scopes: []string{"auth_keys"}, Tags: []string{"tag:ci"}}

	_, bad, ok = narrowTags(scoped, []string{"tag:other"})
	assert.False(t, ok)
	assert.Equal(t, "tag:other", bad)
}

// TestNarrowTagsEdgeCases covers failure-point scenarios around tag narrowing
// that exercise the trust boundary when tokens are minted under unusual client
// configurations.
func TestNarrowTagsEdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		client  *types.OAuthClient
		request []string
		want    []string
		wantBad string
		wantOk  bool
	}{
		{
			name:    "empty request returns client's full tag set",
			client:  &types.OAuthClient{Scopes: []string{"auth_keys"}, Tags: []string{"tag:a", "tag:b"}},
			request: nil,
			want:    []string{"tag:a", "tag:b"},
			wantOk:  true,
		},
		{
			name:    "empty request with empty client tag set returns empty (not nil)",
			client:  &types.OAuthClient{Scopes: []string{"auth_keys"}, Tags: []string{}},
			request: nil,
			want:    []string{},
			wantOk:  true,
		},
		{
			name:    "single valid scoped tag accepted",
			client:  &types.OAuthClient{Scopes: []string{"auth_keys"}, Tags: []string{"tag:ci"}},
			request: []string{"tag:ci"},
			want:    []string{"tag:ci"},
			wantOk:  true,
		},
		{
			name:    "multiple valid scoped tags accepted",
			client:  &types.OAuthClient{Scopes: []string{"auth_keys"}, Tags: []string{"tag:ci", "tag:k8s"}},
			request: []string{"tag:ci", "tag:k8s"},
			want:    []string{"tag:ci", "tag:k8s"},
			wantOk:  true,
		},
		{
			name:    "first invalid tag rejected (partial list)",
			client:  &types.OAuthClient{Scopes: []string{"auth_keys"}, Tags: []string{"tag:ci"}},
			request: []string{"tag:ci", "tag:bad"},
			wantBad: "tag:bad",
			wantOk:  false,
		},
		{
			name:    "duplication in request passes through",
			client:  &types.OAuthClient{Scopes: []string{"auth_keys"}, Tags: []string{"tag:ci"}},
			request: []string{"tag:ci", "tag:ci"},
			want:    []string{"tag:ci", "tag:ci"},
			wantOk:  true,
		},
		{
			name:    "non-tag-prefix rejected by all-scope client",
			client:  &types.OAuthClient{Scopes: []string{"all"}, Tags: nil},
			request: []string{"role:admin"},
			wantBad: "role:admin",
			wantOk:  false,
		},
		{
			name:    "tag with correct prefix but not in client's tag list rejected",
			client:  &types.OAuthClient{Scopes: []string{"auth_keys"}, Tags: []string{"tag:x"}},
			request: []string{"tag:valid_prefix"},
			wantBad: "tag:valid_prefix", // passes prefix check but not member check
			wantOk:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, bad, ok := narrowTags(tt.client, tt.request)
			assert.Equal(t, tt.wantOk, ok, "ok mismatch")
			if !ok {
				assert.Equal(t, tt.wantBad, bad, "bad tag mismatch on rejection")
			} else {
				assert.Equal(t, tt.want, got, "result mismatch on acceptance")
			}
		})
	}
}

// TestNarrowScopes covers the scope-narrowing logic exercised at the OAuth
// token endpoint's trust boundary. An empty request means "use the client's
// full grant"; every requested scope must be within it, otherwise the
// offending scope is returned and the token request is rejected.
func TestNarrowScopes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		granted []string
		request []string
		want    []string
		wantBad string
		wantOk  bool
	}{
		{
			name:    "empty request returns full grant",
			granted: []string{"auth_keys", "devices:core"},
			request: nil,
			want:    []string{"auth_keys", "devices:core"},
			wantOk:  true,
		},
		{
			name:    "empty request with no grants returns empty slice (not nil)",
			granted: []string{},
			request: nil,
			want:    []string{},
			wantOk:  true,
		},
		{
			name:    "nil request returns full grant",
			granted: []string{"policy_file"},
			request: nil,
			want:    []string{"policy_file"},
			wantOk:  true,
		},
		{
			name:    "subset of granted scopes",
			granted: []string{"all"},
			request: []string{"auth_keys:read"},
			want:    []string{"auth_keys:read"},
			wantOk:  true,
		},
		{
			name:    "exact match",
			granted: []string{"oauth_keys"},
			request: []string{"oauth_keys"},
			want:    []string{"oauth_keys"},
			wantOk:  true,
		},
		{
			name:    "ungranted scope rejected",
			granted: []string{"auth_keys"},
			request: []string{"devices:core"},
			wantBad: "devices:core",
			wantOk:  false,
		},
		{
			name:    "mixed valid+invalid rejected at first offender",
			granted: []string{"auth_keys", "devices:core"},
			request: []string{"auth_keys", "policy_file"},
			wantBad: "policy_file",
			wantOk:  false,
		},
		{
			name:    "read-only within write scope accepted",
			granted: []string{"devices:core", "devices:core:read"},
			request: []string{"devices:core:read"},
			want:    []string{"devices:core:read"},
			wantOk:  true,
		},
		{
			name:    "all scope accepts anything",
			granted: []string{"all"},
			request: []string{"auth_keys", "policy_file", "devices:core", "oauth_keys"},
			want:    []string{"auth_keys", "policy_file", "devices:core", "oauth_keys"},
			wantOk:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, bad, ok := narrowScopes(tt.granted, tt.request)
			assert.Equal(t, tt.wantOk, ok, "ok mismatch")
			if !ok {
				assert.Equal(t, tt.wantBad, bad, "bad scope mismatch")
			} else {
				assert.Equal(t, tt.want, got, "result mismatch")
			}
		})
	}
}

// TestNarrowScopesEnforcesScopeContract ensures narrowScopes respects the
// scope.Grants contract — specifically that a read-scoped client cannot be
// granted write authority by requesting both the read and write variants.
func TestNarrowScopesEnforcesScopeContract(t *testing.T) {
	t.Parallel()

	// A client that ONLY has devices:core:read cannot be tricked into
	// also getting devices:core.
	reader := []string{"devices:core:read"}

	result, bad, ok := narrowScopes(reader, []string{"devices:core"})
	assert.False(t, ok, "write scope denied when client only has read")
	assert.Equal(t, "devices:core", bad)

	// Conversely, a writer can explicitly request their own read variant.
	writer := []string{"devices:core", "devices:core:read"}

	result, _, ok = narrowScopes(writer, []string{"devices:core:read"})
	assert.True(t, ok)
	assert.Equal(t, []string{"devices:core:read"}, result)
}

// TestNarrowTagsAllScopeWithNilTags verifies that an "all" client whose Tags
// field is nil (not just empty slice) returns the correct zero value when no
// tags are requested — callers downstream depend on this.
func TestNarrowTagsAllScopeWithNilTags(t *testing.T) {
	t.Parallel()

	allNoTags := &types.OAuthClient{Scopes: []string{"all"}, Tags: nil}

	got, _, ok := narrowTags(allNoTags, nil)
	assert.True(t, ok)
	assert.Nil(t, got, "nil tags + no request → nil result, not empty slice")

	// But a valid tag should work.
	got, _, ok = narrowTags(allNoTags, []string{"tag:anything"})
	assert.True(t, ok)
	assert.Equal(t, []string{"tag:anything"}, got)
}
