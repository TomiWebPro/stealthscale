package util

import (
	"net/netip"
	"testing"

	"go4.org/netipx"
)

func ipSetFromPrefixes(t *testing.T, prefixes ...string) *netipx.IPSet {
	t.Helper()

	var builder netipx.IPSetBuilder
	for _, pre := range prefixes {
		builder.AddPrefix(netip.MustParsePrefix(pre))
	}

	s, err := builder.IPSet()
	if err != nil {
		t.Fatalf("building IPSet from %v: %v", prefixes, err)
	}

	return s
}

func TestIPSetSubsetOf(t *testing.T) {
	tests := []struct {
		name      string
		candidate []string
		container []string
		want      bool
	}{
		{
			name:      "nil candidate fails closed",
			candidate: nil,
			container: []string{"0.0.0.0/0"},
			want:      false,
		},
		{
			name:      "nil container fails closed",
			candidate: []string{"8.8.8.8/32"},
			container: nil,
			want:      false,
		},
		{
			name:      "exact same set is a subset",
			candidate: []string{"10.0.0.0/8"},
			container: []string{"10.0.0.0/8"},
			want:      true,
		},
		{
			name:      "strict subset",
			candidate: []string{"10.1.0.0/16"},
			container: []string{"10.0.0.0/8"},
			want:      true,
		},
		{
			name:      "multi-prefix candidate fully contained",
			candidate: []string{"10.1.0.0/16", "192.168.0.0/24"},
			container: []string{"10.0.0.0/8", "192.168.0.0/16"},
			want:      true,
		},
		{
			name:      "partial overlap is not a subset",
			candidate: []string{"10.0.0.0/8"},
			container: []string{"10.1.0.0/16"},
			want:      false,
		},
		{
			name:      "one uncontained prefix fails the whole candidate",
			candidate: []string{"10.1.0.0/16", "172.16.0.0/12"},
			container: []string{"10.0.0.0/8", "192.168.0.0/16"},
			want:      false,
		},
		{
			name:      "disjoint sets are not subsets",
			candidate: []string{"192.0.2.0/24"},
			container: []string{"198.51.100.0/24"},
			want:      false,
		},
		{
			name:      "empty candidate is a subset of anything",
			candidate: []string{},
			container: []string{"10.0.0.0/8"},
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var candidate, container *netipx.IPSet
			if tt.candidate != nil {
				candidate = ipSetFromPrefixes(t, tt.candidate...)
			}

			if tt.container != nil {
				container = ipSetFromPrefixes(t, tt.container...)
			}

			if got := IPSetSubsetOf(candidate, container); got != tt.want {
				t.Errorf("IPSetSubsetOf(%v, %v) = %v, want %v", tt.candidate, tt.container, got, tt.want)
			}
		})
	}
}

func TestTheInternet(t *testing.T) {
	internet := TheInternet()

	// TheInternet is the destination set autogroup:internet and the
	// exit-node filter reduction reason about. It must cover public
	// space on both families and exclude every non-routable range,
	// so that neither a /0 nor a superset check can treat private
	// or Tailscale-internal traffic as internet-destined.
	contained := []string{
		"8.8.8.8",
		"1.1.1.1",
		"9.9.9.9",
		"2001:db8::1", // within 2000::/3
		"2600::1",
	}
	for _, ip := range contained {
		if !internet.Contains(netip.MustParseAddr(ip)) {
			t.Errorf("TheInternet() should contain %s", ip)
		}
	}

	excluded := []string{
		// RFC1918
		"10.0.0.1",
		"172.16.0.1",
		"192.168.0.1",
		// CGNAT
		"100.64.0.1",
		// link-local
		"169.254.0.1",
		"fe80::1",
		// ULA, including the Tailscale ULA range
		"fc00::1",
		"fd7a:115c:a1e0::1",
	}
	for _, ip := range excluded {
		if internet.Contains(netip.MustParseAddr(ip)) {
			t.Errorf("TheInternet() should not contain %s", ip)
		}
	}

	// A /0 is never contained: the internet set is strictly smaller
	// than either family's full space, which is what keeps the
	// superset-of-TheInternet check in DestsIsTheInternet meaningful.
	if internet.ContainsPrefix(netip.MustParsePrefix("0.0.0.0/0")) {
		t.Error("TheInternet() must not contain 0.0.0.0/0")
	}

	if internet.ContainsPrefix(netip.MustParsePrefix("::/0")) {
		t.Error("TheInternet() must not contain ::/0")
	}

	// A superset of TheInternet (all IPv4 plus all of 2000::/3)
	// passes the subset check in the direction the policy engine
	// uses it: IPSetSubsetOf(TheInternet(), superset).
	superset := ipSetFromPrefixes(t, "0.0.0.0/0", "2000::/3")
	if !IPSetSubsetOf(internet, superset) {
		t.Error("IPSetSubsetOf(TheInternet(), superset-of-internet) = false, want true")
	}

	// A set missing public IPv4 space is not a superset of
	// TheInternet, so a dests set covering only part of the public
	// internet must not report as internet-wide.
	partial := ipSetFromPrefixes(t, "2000::/3", "8.8.8.0/24")
	if IPSetSubsetOf(internet, partial) {
		t.Error("IPSetSubsetOf(TheInternet(), partial-public-set) = true, want false")
	}
}

func TestPrefixesToString(t *testing.T) {
	got := PrefixesToString([]netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("2001:db8::/32"),
	})
	want := []string{"10.0.0.0/8", "2001:db8::/32"}

	if len(got) != len(want) {
		t.Fatalf("PrefixesToString() = %v, want %v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("PrefixesToString()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestMustStringsToPrefixesPanicsOnInvalidInput(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("MustStringsToPrefixes() did not panic on invalid input")
		}
	}()

	MustStringsToPrefixes([]string{"not-a-prefix"})
}
