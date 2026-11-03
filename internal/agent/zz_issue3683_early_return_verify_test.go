package agent

import (
	"os"
	"strings"
	"testing"
)

func Test3683EarlyReturnsSetAsyncVerifyStats(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatalf("read agent.go: %v", err)
	}
	text := string(src)

	assign := "asyncVerifyStats = runStats"
	sites := 0
	for off := 0; ; {
		idx := strings.Index(text[off:], assign)
		if idx < 0 {
			break
		}
		idx += off
		sites++
		lo := idx - 350
		if lo < 0 {
			lo = 0
		}
		hi := idx + 700
		if hi > len(text) {
			hi = len(text)
		}
		window := text[lo:hi]
		hasReturn := strings.Contains(window, "return nil")
		hasGate := strings.Contains(window, "codeChangedInRun(runStats)")
		if sites <= 2 && (!hasReturn || !hasGate) {
			t.Fatalf("guard site %d: gate/return wiring incomplete (gate=%v return=%v) — the early-return verify bypass may be back", sites, hasGate, hasReturn)
		}
		off = idx + len(assign)
	}
	if sites < 3 {
		t.Fatalf("expected >=3 asyncVerifyStats = runStats sites (deadlock guard + strategist Complete guard + main path), found %d", sites)
	}
}
