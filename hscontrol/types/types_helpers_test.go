// Copyright (c) 2026 TomiWebPro <TomiWebPro@gmail.com>
// SPDX-License-Identifier: BSD-3-Clause

package types

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"tailscale.com/tailcfg"
)

func TestNodeIDMethods(t *testing.T) {
	id := NodeID(42)

	assert.Equal(t, "42", id.String())
	assert.Equal(t, uint64(42), id.Uint64())
	assert.True(t, id.StableID() == "42")
	assert.Equal(t, tailcfg.NodeID(42), id.NodeID())

	// StringID works for both nil and non-nil receivers.
	assert.Equal(t, "42", (&Node{ID: id}).StringID())
	assert.Equal(t, "", (*Node)(nil).StringID())
}

func TestParseNodeID(t *testing.T) {
	got, err := ParseNodeID("123")
	require.NoError(t, err)
	assert.Equal(t, NodeID(123), got)

	_, err = ParseNodeID("not-a-number")
	assert.Error(t, err)

	assert.Equal(t, NodeID(7), MustParseNodeID("7"))
	assert.Panics(t, func() { MustParseNodeID("bad") })
}

func TestNamedSliceHelpers(t *testing.T) {
	// Strings: IsZero is always false (GORM zeroer); List returns underlying.
	s := Strings{"a", "b"}
	assert.False(t, s.IsZero())
	assert.Equal(t, []string{"a", "b"}, s.List())

	var empty Strings
	assert.False(t, empty.IsZero())
	assert.Empty(t, empty.List())

	// Prefixes and AddrPorts behave the same.
	p := Prefixes{netip.MustParsePrefix("10.0.0.0/24")}
	assert.False(t, p.IsZero())
	assert.Equal(t, []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")}, p.List())

	ap := AddrPorts{netip.MustParseAddrPort("127.0.0.1:443")}
	assert.False(t, ap.IsZero())
	assert.Equal(t, []netip.AddrPort{netip.MustParseAddrPort("127.0.0.1:443")}, ap.List())
}

func TestNodeOwnership(t *testing.T) {
	tagged := &Node{Tags: Strings{"tag:server"}}
	assert.True(t, tagged.IsTagged())
	assert.False(t, tagged.IsUserOwned())
	assert.True(t, tagged.HasTag("tag:server"))
	assert.False(t, tagged.HasTag("tag:other"))

	userOwned := &Node{}
	assert.False(t, userOwned.IsTagged())
	assert.True(t, userOwned.IsUserOwned())
	assert.False(t, userOwned.HasTag("tag:server"))

	// TypedUserID surfaces the owning user id.
	uid := uint(9)
	withUser := &Node{UserID: &uid}
	assert.Equal(t, UserID(9), withUser.TypedUserID())

	nilUser := &Node{}
	assert.Equal(t, UserID(0), nilUser.TypedUserID())
}

func TestNodeGetFQDN(t *testing.T) {
	node := &Node{GivenName: "alice"}

	fqdn, err := node.GetFQDN("example.com")
	require.NoError(t, err)
	assert.Equal(t, "alice.example.com.", fqdn)

	short, err := node.GetFQDN("")
	require.NoError(t, err)
	assert.Equal(t, "alice", short)

	// Missing given name is an error.
	_, err = (&Node{}).GetFQDN("example.com")
	assert.ErrorIs(t, err, ErrNodeHasNoGivenName)
}
