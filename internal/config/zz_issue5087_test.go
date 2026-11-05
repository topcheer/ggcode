package config

import (
	"os"
	"strings"
	"testing"
)

// #5087: PR #5050's fix (merged via racing #5052) changed the hint parser in
// TWO places - the live parser (internal/config/context_window.go) AND the
// staticCode() generator template (scripts/sync-model-caps.go). The racing
// superseded branch (#5053) carried a sync guard, but its constants anchor its
// OWN implementation shape (simplified pattern + leading-check), which does not
// match the merged #5052 shape (original pattern kept + FindStringSubmatchIndex
// manual scan + resume at end of group 3). This guard pins the MERGED shape in
// both copies so neither a hand-edit divergence nor the next
// `go run scripts/sync-model-caps.go` can silently reintroduce the
// boundary-consuming parser.
func TestIssue5087GeneratorSyncGuard(t *testing.T) {
	const (
		// The manual-scan API both copies must use (non-overlapping-safe).
		manualScan = "FindStringSubmatchIndex"
		// Resume point: end of group 3 (the k/m suffix) - the trailing
		// boundary char is left for the next token's leading boundary.
		resumeInvariant = "i += loc[7]"
		// The consuming call form that must NOT appear. Note: without the
		// trailing '(' this would false-positive on FindAllStringSubmatchIndex.
		consumingCall = "FindAllStringSubmatch("
		// Issue marker comment carried by both copies.
		marker = "#5050"
	)
	for _, path := range []string{
		"context_window.go",
		"../../scripts/sync-model-caps.go",
	} {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("#5087 read %s: %v", path, err)
		}
		code := string(src)
		if !strings.Contains(code, manualScan) {
			t.Errorf("#5087 %s lacks the %s manual-scan API", path, manualScan)
		}
		if !strings.Contains(code, resumeInvariant) {
			t.Errorf("#5087 %s lacks the group-3 resume invariant %q", path, resumeInvariant)
		}
		if strings.Contains(code, consumingCall) {
			t.Errorf("#5087 %s still contains the boundary-consuming call %q", path, consumingCall)
		}
		if !strings.Contains(code, marker) {
			t.Errorf("#5087 %s lacks the %s marker comment", path, marker)
		}
	}
}

// Richer edge-case rows lost with #5053: mixed k->m where the LARGER token
// comes later (4k-1m) - the worst-case underestimate face of the original bug.
func TestIssue5087MixedUnitTokens(t *testing.T) {
	cases := []struct {
		model string
		want  int
	}{
		{"test-4k-1m", 1000000},
		{"test-1m-4k", 1000000}, // later SMALLER loses to earlier larger
		{"8k-128k", 128000},
		{"4k,1m,2m", 2000000},
	}
	for _, tc := range cases {
		if got := parseContextWindowHint(tc.model); got != tc.want {
			t.Errorf("parseContextWindowHint(%q) = %d, want %d", tc.model, got, tc.want)
		}
	}
}
