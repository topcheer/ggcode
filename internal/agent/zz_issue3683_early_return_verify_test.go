package agent

import (
	"os"
	"strings"
	"testing"
)

// #3683: the autopilot deadlock force-terminate and the strategist Complete
// paths used to `return nil` BEFORE the sync verify gate (L~3414) and before
// the only `asyncVerifyStats = runStats` assignment (L~3433), so any code
// changed during the run exited as "success" with asyncVerifyStats forever
// nil — no build, no test, no verification at all. The fix hands runStats to
// the deferred async verification on both paths. This probe pins that wiring
// so a future refactor cannot silently drop the guard again (the original
// author fixed the empty-guidance path but left these two — "fix one, miss
// two").
func Test3683EarlyReturnsSetAsyncVerifyStats(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatalf("read agent.go: %v", err)
	}
	text := string(src)

	// Every #3683 guard site must pair the assignment with the return.
	markers := strings.Count(text, "#3683")
	if markers < 2 {
		t.Fatalf("expected >=2 #3683 guard comments (deadlock + strategist Complete), found %d", markers)
	}

	// The two guarded sites: assignment must appear immediately before the
	// return that follows each #3683 comment block.
	for i, anchor := range []string{
		"#3683: this early return used to skip EVERY completion gate",
		"#3683: same bypass as the deadlock path above",
	} {
		idx := strings.Index(text, anchor)
		if idx < 0 {
			t.Fatalf("guard comment %d not found in agent.go", i)
		}
		window := text[idx : idx+700]
		hasAssign := strings.Contains(window, "asyncVerifyStats = runStats")
		hasReturn := strings.Contains(window, "return nil")
		hasGate := strings.Contains(window, "codeChangedInRun(runStats)")
		if !hasAssign || !hasReturn || !hasGate {
			t.Fatalf("guard site %d: assignment/gate/return wiring incomplete (assign=%v gate=%v return=%v) — the early-return verify bypass may be back", i, hasAssign, hasGate, hasReturn)
		}
	}
}
