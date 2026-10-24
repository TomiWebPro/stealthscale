package apiv2

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestExpiryDurationDefaults(t *testing.T) {
	t.Parallel()

	t.Run("zero_seconds_returns_default_90days", func(t *testing.T) {
		t.Parallel()

		d := expiryDuration(0)
		assert.Equal(t, defaultExpiry, d)
	})

	t.Run("negative_seconds_returns_default", func(t *testing.T) {
		t.Parallel()

		d := expiryDuration(-1)
		assert.Equal(t, defaultExpiry, d, "negative expiry must fall back to default")
	})

	t.Run("positive_seconds_respected", func(t *testing.T) {
		t.Parallel()

		for _, sec := range []int64{1, 60, 3600, 86400, 1826} {
			d := expiryDuration(sec)
			assert.Equal(t, time.Duration(sec)*time.Second, d, "sec=%d", sec)
		}
	})

	t.Run("exactly_one_day", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, 24*time.Hour, expiryDuration(86400))
	})

	t.Run("default_matches_tailscale_convention", func(t *testing.T) {
		t.Parallel()

		// 90 days = 7776000 seconds. Tailscale docs cite 90-day default.
		expectedSecs := int64(90 * 24 * 3600)
		assert.Equal(t, expectedSecs, int64(defaultExpiry.Seconds()))
	})
}



func TestTimeOrZero(t *testing.T) {
	t.Parallel()

	zerotime := time.Time{}

	assert.Equal(t, zerotime, timeOrZero(nil), "nil pointer returns zero time")

	fixed := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	assert.Equal(t, fixed, timeOrZero(&fixed), "pointer returns dereferenced value")
}

func TestExpirySecondsEdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("nil_created_returns_zero", func(t *testing.T) {
		t.Parallel()

		expires := time.Now()
		assert.Zero(t, expirySeconds(nil, &expires))
	})

	t.Run("nil_expires_returns_zero", func(t *testing.T) {
		t.Parallel()

		created := time.Now()
		assert.Zero(t, expirySeconds(&created, nil))
	})

	t.Run("both_nil_returns_zero", func(t *testing.T) {
		t.Parallel()

		assert.Zero(t, expirySeconds(nil, nil))
	})

	t.Run("future_expires_positive_seconds", func(t *testing.T) {
		t.Parallel()

		created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		expires := created.Add(90 * 24 * time.Hour)

		secs := expirySeconds(&created, &expires)
		expected := int64(90*24*3600)
		assert.Equal(t, expected, secs)
	})

	t.Run("past_expires_clamped_to_zero_not_negative", func(t *testing.T) {
		t.Parallel()

		// A key whose expiration is before creation should not produce negative seconds.
		created := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
		expires := created.Add(-1 * time.Hour)

		secs := expirySeconds(&created, &expires)
		assert.Equal(t, int64(0), secs, "negative lifetime must clamp to zero")
	})

	t.Run("sub_second_rounding", func(t *testing.T) {
		t.Parallel()

		// Simulate the stored expiration being a few microseconds before CreatedAt.
		// The math.Round should recover the intended integer seconds.
		created := time.Date(2026, 1, 1, 0, 0, 0, 1000000, time.UTC) // +1ms offset
		expires := created.Add(86400 * time.Second)

		secs := expirySeconds(&created, &expires)
		assert.Equal(t, int64(86400), secs, "sub-ms rounding error must not flip day boundary")
	})

	t.Run("exact_30_day_lifetime", func(t *testing.T) {
		t.Parallel()

		created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		expires := created.Add(30 * 24 * time.Hour)

		secs := expirySeconds(&created, &expires)
		assert.Equal(t, int64(30*24*3600), secs)
	})
}
