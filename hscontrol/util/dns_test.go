package util

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"tailscale.com/util/dnsname"
	"tailscale.com/util/must"
)

// TestValidateUsernameFailureModes covers every error path of
// ValidateUsername — length, leading character, '@' count, and invalid
// characters. A regression that accepts any string (or rejects valid
// hostnames via panic on empty input) must fail this suite.
func TestValidateUsernameFailureModes(t *testing.T) {
	t.Parallel()

	t.Run("single_character_rejected", func(t *testing.T) {
		t.Parallel()

		err := ValidateUsername("a")
		assert.ErrorIs(t, err, ErrUsernameTooShort, "single char must be too short")
	})

	t.Run("empty_string_rejected", func(t *testing.T) {
		t.Parallel()

		err := ValidateUsername("")
		assert.ErrorIs(t, err, ErrUsernameTooShort)
	})

	t.Run("starts_with_number_rejected", func(t *testing.T) {
		t.Parallel()

		err := ValidateUsername("1abc")
		assert.ErrorIs(t, err, ErrUsernameMustStartLetter)
	})

	t.Run("starts_with_hyphen_rejected", func(t *testing.T) {
		t.Parallel()

		err := ValidateUsername("-admin")
		assert.ErrorIs(t, err, ErrUsernameMustStartLetter)
	})

	t.Run("starts_with_underscore_rejected", func(t *testing.T) {
		t.Parallel()

		err := ValidateUsername("_svc")
		assert.ErrorIs(t, err, ErrUsernameMustStartLetter)
	})

	t.Run("double_at_sign_rejected", func(t *testing.T) {
		t.Parallel()

		err := ValidateUsername("user@@domain")
		assert.ErrorIs(t, err, ErrUsernameTooManyAt)
	})

	t.Run("emoji_in_username_rejected", func(t *testing.T) {
		t.Parallel()

		err := ValidateUsername("👋hello")
		assert.ErrorContains(t, err, ErrUsernameInvalidChar.Error())
	})

	t.Run("newline_in_username_rejected", func(t *testing.T) {
		t.Parallel()

		err := ValidateUsername("user\nname")
		assert.ErrorContains(t, err, ErrUsernameInvalidChar.Error())
	})

	t.Run("space_in_username_rejected", func(t *testing.T) {
		t.Parallel()

		err := ValidateUsername("user name")
		assert.ErrorContains(t, err, ErrUsernameInvalidChar.Error())
	})

	t.Run("slash_in_username_rejected", func(t *testing.T) {
		t.Parallel()

		err := ValidateUsername("u/sr")
		assert.ErrorContains(t, err, ErrUsernameInvalidChar.Error())
	})

	t.Run("percent_in_username_rejected", func(t *testing.T) {
		t.Parallel()

		err := ValidateUsername("us%r")
		assert.ErrorContains(t, err, ErrUsernameInvalidChar.Error())
	})

	t.Run("unicode_cyrillic_start_accepted", func(t *testing.T) {
		// Letters from other scripts are accepted by Go's unicode.IsLetter.
		// ValidateUsername delegates to unicode.IsLetter — what matters is
		// consistency between what OIDC claims produce and what we validate.
		t.Parallel()

		err := ValidateUsername("привет")
		assert.NoError(t, err)
	})

	t.Run("two_char_user_accepted", func(t *testing.T) {
		t.Parallel()

		err := ValidateUsername("ab")
		assert.NoError(t, err)
	})

	t.Run("email_style_accepted", func(t *testing.T) {
		t.Parallel()

		err := ValidateUsername("user@domain")
		assert.NoError(t, err)
	})

	t.Run("dots_and_hyphens_accepted", func(t *testing.T) {
		t.Parallel()

		err := ValidateUsername("user.name-here_test")
		assert.NoError(t, err)
	})

	t.Run("hyphen_second_position_accepted", func(t *testing.T) {
		t.Parallel()

		err := ValidateUsername("-")
		// Single char = too short (not leading-char error), since the
		// length check runs first in ValidateUsername.
		assert.ErrorIs(t, err, ErrUsernameTooShort)
	})
}

// TestValidateUsernameHappyPath confirms common real-world usernames all pass.
func TestValidateUsernameHappyPath(t *testing.T) {
	t.Parallel()

	validNames := []string{
		"alice",
		"user01",
		"john.doe@example.com",
		"a-b-c",
		"user_name",
	}

	for _, name := range validNames {
		err := ValidateUsername(name)
		assert.NoErrorf(t, err, "%q should be valid", name)
	}
}

// TestValidateUsernameEdgeCases checks that edge inputs never cause a panic —
// even if they are rejected as invalid. These inputs come from different parts
// of the codebase and would surface as nil-pointer derefs or crashes.
func TestValidateUsernameEdgeCases(t *testing.T) {
	t.Parallel()

	edgeInputs := []string{
		"",        // nil OIDC preferred_username
		"\x00",    // binary noise
		"\n\t\r",  // whitespace-only
		"ab\x00c", // embedded NUL
	}

	for _, in := range edgeInputs {
		err := ValidateUsername(in)
		assert.Error(t, err, "%q must return an error, not panic", in)
	}
}

func TestMagicDNSRootDomains100(t *testing.T) {
	domains := GenerateIPv4DNSRootDomain(netip.MustParsePrefix("100.64.0.0/10"))

	assert.Contains(t, domains, must.Get(dnsname.ToFQDN("64.100.in-addr.arpa.")))
	assert.Contains(t, domains, must.Get(dnsname.ToFQDN("100.100.in-addr.arpa.")))
	assert.Contains(t, domains, must.Get(dnsname.ToFQDN("127.100.in-addr.arpa.")))
}

func TestMagicDNSRootDomains172(t *testing.T) {
	domains := GenerateIPv4DNSRootDomain(netip.MustParsePrefix("172.16.0.0/16"))

	assert.Contains(t, domains, must.Get(dnsname.ToFQDN("0.16.172.in-addr.arpa.")))
	assert.Contains(t, domains, must.Get(dnsname.ToFQDN("255.16.172.in-addr.arpa.")))
}

// Happens when netmask is a multiple of 4 bits (sounds likely).
func TestMagicDNSRootDomainsIPv6Single(t *testing.T) {
	domains := GenerateIPv6DNSRootDomain(netip.MustParsePrefix("fd7a:115c:a1e0::/48"))

	assert.Len(t, domains, 1)
	assert.Equal(t, "0.e.1.a.c.5.1.1.a.7.d.f.ip6.arpa.", domains[0].WithTrailingDot())
}

func TestMagicDNSRootDomainsIPv6SingleMultiple(t *testing.T) {
	domains := GenerateIPv6DNSRootDomain(netip.MustParsePrefix("fd7a:115c:a1e0::/50"))

	yieldsRoot := func(dom string) bool {
		for _, candidate := range domains {
			if candidate.WithTrailingDot() == dom {
				return true
			}
		}

		return false
	}

	assert.Len(t, domains, 4)
	assert.True(t, yieldsRoot("0.0.e.1.a.c.5.1.1.a.7.d.f.ip6.arpa."))
	assert.True(t, yieldsRoot("1.0.e.1.a.c.5.1.1.a.7.d.f.ip6.arpa."))
	assert.True(t, yieldsRoot("2.0.e.1.a.c.5.1.1.a.7.d.f.ip6.arpa."))
	assert.True(t, yieldsRoot("3.0.e.1.a.c.5.1.1.a.7.d.f.ip6.arpa."))
}
