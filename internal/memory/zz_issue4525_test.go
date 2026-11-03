//go:build goolm

package memory

import (
	"os"
	"strings"
	"testing"
)

func TestIssue4525_ForgottenSetReadUnderLock(t *testing.T) {
	b, err := os.ReadFile("gc.go")
	if err != nil {
		t.Skip("source not readable in this environment")
	}
	s := string(b)
	lockIdx := strings.Index(s, `unlockGC, lockErr := util.FileLock(am.dir + ".lock")`)
	readIdx := strings.Index(s, "forgotten := am.ForgottenSet()")
	if lockIdx < 0 || readIdx < 0 {
		t.Fatal("gc.go structure changed: lock acquisition or forgotten read not found")
	}
	if readIdx < lockIdx {
		t.Fatal("#4525 regression: ForgottenSet read happens before the FileLock")
	}
}
