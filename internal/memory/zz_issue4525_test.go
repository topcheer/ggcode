//go:build goolm

package memory

import (
	"os"
	"strings"
	"testing"
)

// #4525 pin: GC must read the forgotten set AFTER acquiring the directory
// FileLock. The old order (read → lock) let a RestoreKey complete in the
// lock-free window and still get archived+tombstoned by the stale snapshot -
// RestoreKey only touches the sidecar, so the md mtime guard never saw it.
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
