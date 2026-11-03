package agent

import (
	"strings"
	"testing"
)

func TestSecretScanWindowNoPanic(t *testing.T) {
	for _, n := range []int{maxSecretScanLen + 1, maxSecretScanLen + 32, maxSecretScanLen + 64, maxSecretScanLen + 65} {
		newC := strings.Repeat("a", n)
		oldC := strings.Repeat("a", n-10)

		_ = checkHardcodedSecrets("x.go", oldC, newC)
	}
}

func TestSecretScanHeadNotReintroduced(t *testing.T) {
	const secret = "AKIAIOSFODNN7EXAMPLE"
	head := "// doc example key: " + secret + " end\n"
	pad := strings.Repeat("b", 300*1024)
	oldC := head + pad + "old tail"
	newC := head + pad + "new tail"
	warnings := checkHardcodedSecrets("x.go", oldC, newC)
	for _, w := range warnings {
		if strings.Contains(w, secret) {
			t.Errorf("head-seated pre-existing secret reported as new: %s", w)
		}
	}
}
