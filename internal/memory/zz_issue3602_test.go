package memory

// #3602 probe: ForgetUsage must hold the #3120 cross-process FileLock
// around its sidecar read-modify-write (FileLock outer, am.mu inner -
// the same contract RecordUse/RecordConsumption follow). The functional
// ghost-drop behavior is pinned here; the lock contract is pinned by the
// source-scan probe below (AST-free: direct structural checks).

import (
	"os"
	"strings"
	"testing"
)

func TestIssue3602_ForgetUsageDropsGhostOnDisk(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}
	forceUses(t, am, "ghost-impl", 3)

	am.ForgetUsage("ghost-impl")

	if _, ok := am.UsageOf("ghost-impl"); ok {
		t.Fatal("ghost entry still served after ForgetUsage")
	}
	// The sidecar on disk must reflect the drop (fresh process view).
	fresh := &AutoMemory{dir: dir}
	if _, ok := fresh.UsageOf("ghost-impl"); ok {
		t.Fatal("ghost entry resurrected from disk after ForgetUsage")
	}
}

func TestIssue3602_ForgetUsageHoldsFileLockContract(t *testing.T) {
	src, err := os.ReadFile("usage.go")
	if err != nil {
		t.Fatal(err)
	}
	code := string(src)
	start := strings.Index(code, "func (am *AutoMemory) ForgetUsage(")
	if start < 0 {
		t.Fatal("ForgetUsage not found - probe anchor drifted")
	}
	end := strings.Index(code[start:], "\n}\n")
	body := code[start : start+end]

	lockAt := strings.Index(body, "util.FileLock(am.dir")
	muAt := strings.Index(body, "am.mu.Lock()")
	if lockAt < 0 {
		t.Fatal("ForgetUsage does not take the #3120 cross-process FileLock (#3602)")
	}
	if muAt >= 0 && muAt < lockAt {
		t.Fatal("ForgetUsage takes am.mu BEFORE the FileLock - wrong lock order (deadlock-prone inversion)")
	}
}
