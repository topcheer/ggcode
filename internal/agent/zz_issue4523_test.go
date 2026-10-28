//go:build goolm

package agent

import (
	"os"
	"strings"
	"testing"
)

// #4523: an explicit oracle SKIP must reach the callers as the
// skipVerifyCommand sentinel (not ""), and both call sites must honor it
// BEFORE the detectBuildSystem fallback - the old code overrode a deliberate
// "markdown-only edits need no verification" with a full make verify-ci.
func TestIssue4523_ExplicitSkipBypassesFallback(t *testing.T) {
	b, err := os.ReadFile("verify.go")
	if err != nil {
		t.Skip("source not readable in this environment")
	}
	s := string(b)
	// The decision function must only sentinel an EXPLICIT SKIP.
	if !strings.Contains(s, `EqualFold(cmd, "SKIP")`) || !strings.Contains(s, "return skipVerifyCommand") {
		t.Fatal("#4523 regression: explicit SKIP no longer returns the sentinel")
	}
	// Empty output / provider failure must still return "" (fallback path).
	if strings.Contains(s, `EqualFold(cmd, "SKIP") || cmd == ""`) {
		t.Fatal("#4523 regression: SKIP conflated with empty output again")
	}
	// Both call sites must consume the sentinel before detectBuildSystem.
	sentinelChecks := strings.Count(s, "cmd == skipVerifyCommand")
	if sentinelChecks < 2 {
		t.Fatalf("#4523 regression: expected sentinel checks at both call sites, found %d", sentinelChecks)
	}
	for _, site := range []string{"async: skipping - oracle decided no verification applies", "sync: skipping - oracle decided no verification applies"} {
		if !strings.Contains(s, site) {
			t.Fatalf("#4523 regression: skip exit missing: %s", site)
		}
	}
}
