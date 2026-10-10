package state

import (
	"testing"
	"time"

	"github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
)

// TestAuthCacheBoundedLRU verifies that the registration auth cache is
// bounded by a maximum entry count, that exceeding the maxEntries evicts the
// oldest entry, and that the eviction callback resolves the parked
// AuthRequest with ErrRegistrationExpired so any waiting goroutine wakes.
func TestAuthCacheBoundedLRU(t *testing.T) {
	const maxEntries = 4

	cache := expirable.NewLRU[types.AuthID, *types.AuthRequest](
		maxEntries,
		func(_ types.AuthID, rn *types.AuthRequest) {
			rn.FinishAuth(types.AuthVerdict{Err: ErrRegistrationExpired})
		},
		time.Hour, // long TTL — we test eviction by size, not by time
	)

	entries := make([]*types.AuthRequest, 0, maxEntries+1)
	ids := make([]types.AuthID, 0, maxEntries+1)

	for range maxEntries + 1 {
		id := types.MustAuthID()
		entry := types.NewAuthRequest()
		cache.Add(id, entry)
		ids = append(ids, id)
		entries = append(entries, entry)
	}

	// Cap should be respected.
	assert.Equal(t, maxEntries, cache.Len(), "cache must not exceed the configured maxEntries")

	// The oldest entry must have been evicted.
	_, ok := cache.Get(ids[0])
	assert.False(t, ok, "oldest entry must be evicted when maxEntries is exceeded")

	// The eviction callback must have woken the parked AuthRequest.
	select {
	case verdict := <-entries[0].WaitForAuth():
		require.False(t, verdict.Accept(), "evicted entry must not signal Accept")
		require.ErrorIs(t,
			verdict.Err, ErrRegistrationExpired,
			"evicted entry must surface ErrRegistrationExpired, got: %v",
			verdict.Err,
		)
	case <-time.After(time.Second):
		t.Fatal("eviction callback did not wake the parked AuthRequest")
	}

	// All non-evicted entries must still be retrievable.
	for i := 1; i <= maxEntries; i++ {
		_, ok := cache.Get(ids[i])
		assert.True(t, ok, "non-evicted entry %d should still be in the cache", i)
	}
}

// TestAuthCacheTimeExpiryWakesParkedRegistration pins the
// time-based expiry path of the registration auth cache — the
// mechanism production actually relies on (the
// registerCacheExpiration TTL, default 15m, tunable via
// Tuning.RegisterCacheExpiration). A registration session that
// lapses past its TTL must wake any goroutine parked on
// WaitForAuth with ErrRegistrationExpired via the cache's
// eviction callback, and the expired session must be gone from
// the cache. This is the boundary the interactive `stscale up`
// registration poll depends on: a regression that drops the TTL
// wiring (a zero TTL disables the expiry janitor entirely, so a
// parked registration would hang until the 1024-entry size
// bound evicts it) or the eviction callback would leave every
// size-eviction test green while real registration sessions
// hang instead of expiring.
func TestAuthCacheTimeExpiryWakesParkedRegistration(t *testing.T) {
	dbPath := t.TempDir() + "/auth-cache-expiry.db"
	cfg := persistTestConfig(dbPath)
	// Short TTL so the expiry janitor (ttl/100 tick) retires the
	// entry within the test budget; the production default is 15m.
	cfg.Tuning.RegisterCacheExpiration = 400 * time.Millisecond

	s, err := NewState(cfg)
	require.NoError(t, err)

	t.Cleanup(func() { _ = s.Close() })

	authID := types.MustAuthID()
	req := types.NewAuthRequest()
	s.SetAuthCacheEntry(authID, req)

	// Before the TTL lapses the session is live.
	got, ok := s.GetAuthCacheEntry(authID)
	require.True(t, ok, "a freshly parked registration session must be retrievable")
	require.Same(t, req, got)

	// The TTL lapse must wake the parked waiter with
	// ErrRegistrationExpired — never hang until the size bound
	// evicts the entry.
	select {
	case verdict := <-req.WaitForAuth():
		require.False(t, verdict.Accept(), "an expired registration session must not be accepted")
		require.ErrorIs(t, verdict.Err, ErrRegistrationExpired,
			"an expired registration session must surface ErrRegistrationExpired, got: %v", verdict.Err)
	case <-time.After(5 * time.Second):
		t.Fatal("registration session was not woken by TTL expiry")
	}

	// After the expiry the session is gone from the cache.
	_, ok = s.GetAuthCacheEntry(authID)
	assert.False(t, ok, "an expired registration session must not remain retrievable")
}
