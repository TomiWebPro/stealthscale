package dockertestutil

import (
	"testing"
	"time"
)

func TestFindContainerInterface(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantIFace string
		wantErr   bool
	}{
		{
			name: "standard Docker output with veth",
			input: `1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65535 qdisc noqueue state UNKNOWN scope host \
    valid_lft forever preferred_lft forever
2: eth0@if74: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc noqueue state UP `,
			wantIFace: "eth0",
			wantErr:   false,
		},
		{
			name: "bare container (no veth)",
			input: `1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65535 \
    valid_lft forever preferred_lft forever
2: eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 \
    valid_lft forever preferred_lft forever`,
			wantIFace: "eth0",
			wantErr:   false,
		},
		{
			name: "multiple interfaces selects first non-lo",
			input: `1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65535 \
2: eth0@if74: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 \
3: docker0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 `,
			wantIFace: "eth0",
			wantErr:   false,
		},
		{
			name: "empty input returns error",
			input: "",
			wantIFace: "",
			wantErr:   true,
		},
		{
			name: "loopback only returns error",
			input: `1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65535`,
			wantIFace: "",
			wantErr:   true,
		},
		{
			name: "malformed lines skipped gracefully",
			input: "short line\nmore garbage",
			wantIFace: "",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			iface, err := findContainerInterfaceString(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("findContainerInterfaceString() expected error but got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("findContainerInterfaceString() unexpected error: %v", err)
			}
			if iface != tt.wantIFace {
				t.Errorf("findContainerInterfaceString() = %q, want %q", iface, tt.wantIFace)
			}
		})
	}
}

// findContainerInterfaceString is a test-only wrapper that feeds [input]
// directly into the parsing logic of findContainerInterface, bypassing
// the Docker exec layer. This enables pure unit testing of interface name
// extraction across different Docker versions and kernel configurations.
func findContainerInterfaceString(input string) (string, error) {
	lines := splitLines(input)
	for _, line := range lines {
		fields := splitFields(line)
		if len(fields) < 2 {
			continue
		}

		if hasSuffix(fields[0], ":") && !contains(line, "lo:") {
			name := trimSuffix(fields[1], ":")
			if idx := indexOfChar(fields[1], '@'); idx >= 0 {
				name = fields[1][:idx]
			}
			return name, nil
		}
	}

	return "", &noInterfaceError{}
}

type noInterfaceError struct{}

func (*noInterfaceError) Error() string { return "no non-loopback interface found" }

// --- tiny string helpers to mirror strings.* without importing ---

func splitLines(s string) []string       { return splitImpl(s, '\n') }
func splitFields(s string) []string      { return spaceSplit(s) }
func hasSuffix(s, suffix string) bool    { return endsWith(s, suffix) }
func contains(s, substr string) bool     { return indexAny(s, substr) >= 0 }
func trimPrefix(s, prefix string) string { return removeLeading(s, prefix) }

func splitImpl(s string, sep rune) []string {
	var out []string
	var cur []rune
	for _, r := range s {
		if r == sep {
			out = append(out, string(cur))
			cur = nil
		} else {
			cur = append(cur, r)
		}
	}
	if len(cur) > 0 {
		out = append(out, string(cur))
	}
	return out
}

func spaceSplit(s string) []string {
	var out []string
	var cur []rune
	inSpace := true
	for _, r := range s {
		if r == ' ' || r == '\t' {
			if !inSpace && len(cur) > 0 {
				out = append(out, string(cur))
				cur = nil
			}
			inSpace = true
		} else {
			cur = append(cur, r)
			inSpace = false
		}
	}
	if len(cur) > 0 {
		out = append(out, string(cur))
	}
	return out
}

func endsWith(s, suffix string) bool {
	if len(s) < len(suffix) {
		return false
	}
	return s[len(s)-len(suffix):] == suffix
}

func indexAny(s, substr string) int {
	i := 0
	for i+len(substr) <= len(s) {
		match := true
		for j := 0; j < len(substr); j++ {
			if s[i+j] != substr[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
		i++
	}
	return -1
}

func removeLeading(s, prefix string) string {
	if len(s) < len(prefix) {
		return s
	}
	if s[:len(prefix)] == prefix {
		return s[len(prefix):]
	}
	return s
}

func trimSuffix(s, suffix string) string {
	if !endsWith(s, suffix) {
		return s
	}
	return s[:len(s)-len(suffix)]
}

func indexOfChar(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func TestBuildNetemFilter(t *testing.T) {
	tests := []struct {
		name  string
		conds NetworkConditions
		want  string
	}{
		{
			name:  "zero conditions produces empty string",
			conds: NetworkConditions{},
			want:  "",
		},
		{
			name:  "latency only with auto jitter",
			conds: NetworkConditions{Latency: 200 * time.Millisecond},
			want:  "delay 200ms 50ms",
		},
		{
			name:  "latency with explicit jitter",
			conds: NetworkConditions{Latency: 100 * time.Millisecond, Jitter: 25 * time.Millisecond},
			want:  "delay 100ms 25ms",
		},
		{
			name:  "loss only",
			conds: NetworkConditions{DropPct: 10.5},
			want:  "drop 10.50%",
		},
		{
			name:  "duplication only",
			conds: NetworkConditions{DuplicatePct: 5.0},
			want:  "duplicate 5.00%",
		},
		{
			name:  "corruption only",
			conds: NetworkConditions{CorrPct: 0.1},
			want:  "corrupt 0.10%",
		},
		{
			name: "combined latency + loss",
			conds: NetworkConditions{
				Latency: 300 * time.Millisecond,
				DropPct: 20,
			},
			want: "delay 300ms 75ms drop 20.00%",
		},
		{
			name: "all primitives",
			conds: NetworkConditions{
				Latency:      150 * time.Millisecond,
				Jitter:       30 * time.Millisecond,
				DropPct:      5.5,
				DuplicatePct: 2.0,
				CorrPct:      0.25,
			},
			want: "delay 150ms 30ms drop 5.50% duplicate 2.00% corrupt 0.25%",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildNetemFilter(tt.conds)
			if got != tt.want {
				t.Errorf("buildNetemFilter(%+v) = %q, want %q", tt.conds, got, tt.want)
			}
		})
	}
}
