package agent

import (
	"strings"
	"testing"
)

// TestSecretScanWindowNoPanic verifies the (256KB, 256KB+64] length window
// that previously panicked on slice bounds (silently disabling the check
// via the per-check recover) (#263).
func TestSecretScanWindowNoPanic(t *testing.T) {
	for _, n := range []int{maxSecretScanLen + 1, maxSecretScanLen + 32, maxSecretScanLen + 64, maxSecretScanLen + 65} {
		newC := strings.Repeat("a", n)
		oldC := strings.Repeat("a", n-10)
		// Must not panic; result presence is not the point here.
		_ = checkHardcodedSecrets("x.go", oldC, newC)
	}
}

// TestSecretScanHeadNotReintroduced verifies that a secret-shaped string at
// the HEAD of a large file, present in both old and new content, is not
// reported as newly introduced when only the tail was edited (#264).
func TestSecretScanHeadNotReintroduced(t *testing.T) {
	const secret = "AKIAIOSFODNN7EXAMPLE"
	head := "// doc example key: " + secret + " end\n"
	pad := strings.Repeat("b", 300*1024) // > 256KB window
	oldC := head + pad + "old tail"
	newC := head + pad + "new tail"
	warnings := checkHardcodedSecrets("x.go", oldC, newC)
	for _, w := range warnings {
		if strings.Contains(w, secret) {
			t.Errorf("head-seated pre-existing secret reported as new: %s", w)
		}
	}
}
