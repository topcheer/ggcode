package tui

import (
	"strings"
	"testing"
)

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

	if _, err := parseIntPositive("99999999999999999999999"); err == nil {
		t.Fatal("over-range bare number must error (via the #903 round-trip check)")
	}

	for s, want := range map[string]int{"1G": 1073741824, "512M": 536870912, "4K": 4096, "4096": 4096} {
		got, err := parseIntPositive(s)
		if err != nil || got != want {
			t.Fatalf("parseIntPositive(%q) = (%d, %v), want (%d, nil)", s, got, err, want)
		}
	}
}
