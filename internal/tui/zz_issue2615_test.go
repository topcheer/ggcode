package tui

import (
	"strings"
	"testing"
)

// #2615: suffix multiplication must not overflow. The two values from the
// issue report: 999999999999G wraps to a large wrong positive (silently
// persisted, crippling auto-compaction) and 8589934593G wraps negative.
// Both must be rejected with an overflow error, not "applied".
func TestParseIntPositiveOverflowRejected(t *testing.T) {
	for _, s := range []string{"999999999999G", "8589934593G"} {
		v, err := parseIntPositive(s)
		if err == nil {
			t.Fatalf("parseIntPositive(%q) = %d, want overflow error", s, v)
		}
		if !strings.Contains(err.Error(), "overflow") {
			t.Fatalf("parseIntPositive(%q) error = %q, want overflow mention", s, err.Error())
		}
	}
	// A bare over-range digit string (no suffix) is already rejected by the
	// #903 round-trip check with a different message - just assert it errors.
	if _, err := parseIntPositive("99999999999999999999999"); err == nil {
		t.Fatal("over-range bare number must error (via the #903 round-trip check)")
	}
	// Sanity: legal values including suffixes still parse.
	for s, want := range map[string]int{"1G": 1073741824, "512M": 536870912, "4K": 4096, "4096": 4096} {
		got, err := parseIntPositive(s)
		if err != nil || got != want {
			t.Fatalf("parseIntPositive(%q) = (%d, %v), want (%d, nil)", s, got, err, want)
		}
	}
}
