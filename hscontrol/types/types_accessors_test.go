// Copyright (c) 2026 TomiWebPro <TomiWebPro@gmail.com>
// SPDX-License-Identifier: BSD-3-Clause

package types

import (
	"database/sql"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"tailscale.com/tailcfg"
)

func TestUserAccessors(t *testing.T) {
	created := time.Now()
	u := &User{
		Name:               "alice",
		DisplayName:        "Alice A",
		Email:              "alice@example.com",
		Provider:           "oidc",
		ProfilePicURL:      "https://img/me.png",
		ProviderIdentifier: sql.NullString{String: "oidc|alice", Valid: true},
	}
	u.ID = 7
	u.CreatedAt = created

	// Username falls back through email > name > provider id > id.
	assert.Equal(t, "alice@example.com", u.Username())
	assert.Equal(t, "Alice A", u.Display())
	assert.Equal(t, "7", u.StringID())
	require.NotNil(t, u.TypedID())
	assert.Equal(t, UserID(7), *u.TypedID())

	tsUser := u.TailscaleUser()
	assert.Equal(t, tailcfg.UserID(7), tsUser.ID)
	assert.Equal(t, "Alice A", tsUser.DisplayName)

	tsLogin := u.TailscaleLogin()
	assert.Equal(t, "alice@example.com", tsLogin.LoginName)

	tsProfile := u.TailscaleUserProfile()
	assert.Equal(t, tailcfg.UserID(7), tsProfile.ID)

	// View accessors mirror the underlying user.
	v := u.View()
	assert.Equal(t, "alice@example.com", v.Username())
	assert.Equal(t, "Alice A", v.Display())
	assert.Equal(t, uint(7), v.ID())
	assert.Equal(t, created, v.CreatedAt())
	assert.Equal(t, tailcfg.UserID(7), v.TailscaleUser().ID)

	// Username falls back to the numeric ID when nothing else is set.
	empty := &User{}
	empty.ID = 3
	assert.Equal(t, "3", empty.View().Username())
}

func TestUserNilReceiver(t *testing.T) {
	var u *User
	assert.Equal(t, "", u.StringID())
	// Username is not nil-safe by design; only StringID guards the receiver.
}

func TestPreAuthKeyAccessors(t *testing.T) {
	uid := uint(4)
	pak := &PreAuthKey{
		ID:     12,
		Prefix: "abc",
		Tags:   []string{"tag:server"},
		UserID: &uid,
	}
	assert.Equal(t, "12", pak.StringID())
	assert.True(t, pak.IsTagged())
	assert.Equal(t, "hskey-auth-abc-***", pak.maskedPrefix())

	// nil receiver
	var np *PreAuthKey
	assert.Equal(t, "", np.StringID())

	// Validate: tagged reusable key is valid.
	require.NoError(t, pak.Validate())

	// revoked
	now := time.Now()
	revoked := &PreAuthKey{ID: 1, Revoked: &now}
	assert.Error(t, revoked.Validate())

	// expired
	expired := &PreAuthKey{ID: 2, Expiration: ptrTime(now.Add(-time.Hour))}
	assert.Error(t, expired.Validate())

	// used and not reusable
	used := &PreAuthKey{ID: 3, Used: true}
	assert.Error(t, used.Validate())

	// reusable and used is fine
	reusable := &PreAuthKey{ID: 4, Used: true, Reusable: true}
	require.NoError(t, reusable.Validate())

	// nil key
	assert.Error(t, (*PreAuthKey)(nil).Validate())
}

func TestNodeViewAccessors(t *testing.T) {
	v4 := netip.MustParseAddr("100.64.1.5")
	v6 := netip.MustParseAddr("fd7a:1::5")

	withUser := (&Node{ID: 1, UserID: ptrUint(9), IPv4: &v4, IPv6: &v6}).View()
	assert.Equal(t, tailcfg.UserID(9), withUser.TailscaleUserID())

	prefixes := withUser.Prefixes()
	assert.Len(t, prefixes, 2)
	assert.Equal(t, "100.64.1.5/32", prefixes[0].String())

	ips := withUser.IPsAsString()
	assert.Equal(t, []string{"100.64.1.5", "fd7a:1::5"}, ips)

	// tagged node resolves to the TaggedDevices user, not zero.
	tagged := (&Node{ID: 2, Tags: Strings{"tag:x"}, IPv4: &v4}).View()
	assert.NotEqual(t, tailcfg.UserID(0), tagged.TailscaleUserID())

	// node with no user and not tagged resolves to zero.
	orphan := (&Node{ID: 3, IPv4: &v4}).View()
	assert.Equal(t, tailcfg.UserID(0), orphan.TailscaleUserID())

	// HasNetworkChanges detects IP differences.
	other := (&Node{ID: 4, IPv4: &v4, IPv6: &v6}).View()
	assert.False(t, withUser.HasNetworkChanges(other))
	assert.True(t, withUser.HasNetworkChanges((&Node{ID: 5, IPv4: &v4}).View()))
}

func ptrUint(u uint) *uint { return &u }
func ptrTime(t time.Time) *time.Time {
	return &t
}
