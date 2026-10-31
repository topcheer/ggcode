package tool

import (
	"strings"
	"testing"
)

// Trust-cascade defense (EMNLP 2026 provenance-erasure finding): the
// delegation boundary must attribute a result's external-source usage.
func TestProvenanceHeaderAggregates(t *testing.T) {
	h := provenanceHeader(map[string]int{"web_fetch": 3, "browser": 1})
	if !strings.Contains(h, "4 external-source call(s)") {
		t.Fatalf("must aggregate the total, got %q", h)
	}
	if !strings.Contains(h, "web_fetch x3") || !strings.Contains(h, "browser x1") {
		t.Fatalf("must list per-tool counts, got %q", h)
	}
	if !strings.HasPrefix(h, "[provenance:") {
		t.Fatalf("header form, got %q", h)
	}
}

func TestProvenanceHeaderEmptyNoChange(t *testing.T) {
	if provenanceHeader(nil) != "" || provenanceHeader(map[string]int{}) != "" {
		t.Fatal("no external sources must produce zero header (common case unchanged)")
	}
}

// Deterministic rendering (sorted) regardless of map iteration order.
func TestProvenanceHeaderDeterministic(t *testing.T) {
	a := provenanceHeader(map[string]int{"z_tool": 1, "a_tool": 1})
	b := provenanceHeader(map[string]int{"a_tool": 1, "z_tool": 1})
	if a != b {
		t.Fatalf("header must be deterministic:\n%q\n%q", a, b)
	}
}
