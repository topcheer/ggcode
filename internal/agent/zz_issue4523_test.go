//go:build goolm

package agent

import (
	"os"
	"strings"
	"testing"
)

func TestIssue4523_ExplicitSkipBypassesFallback(t *testing.T) {
	b, err := os.ReadFile("verify.go")
	if err != nil {
		t.Skip("source not readable in this environment")
	}
	s := string(b)

	if !strings.Contains(s, `EqualFold(cmd, "SKIP")`) || !strings.Contains(s, "return skipVerifyCommand") {
		t.Fatal("#4523 regression: explicit SKIP no longer returns the sentinel")
	}

	if strings.Contains(s, `EqualFold(cmd, "SKIP") || cmd == ""`) {
		t.Fatal("#4523 regression: SKIP conflated with empty output again")
	}

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
