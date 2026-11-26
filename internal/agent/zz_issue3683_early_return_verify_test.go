package agent

import (
	"strings"
	"testing"
)

func Test3683EarlyReturnsSetAsyncVerifyStats(t *testing.T) {
	src, err := readAgentAndProcessingSources()
	if err != nil {
		t.Fatalf("read agent.go: %v", err)
	}
	text := string(src)

	assign := "st.asyncVerifyStats = runStats"
	sites := 0
	for off := 0; ; {
		idx := strings.Index(text[off:], assign)
		if idx < 0 {
			break
		}
		idx += off
		sites++
		// Every site but the last is an early return (the last one is the main
		// no-tool path); each early return must be gated by codeChangedInRun and
		// actually return. The former autopilot deadlock-guard site was removed
		// (the AI decides when to stop), so the early returns left are the
		// strategist "goal achieved" guards.
		if idx+700 < len(text) && strings.Contains(text[idx+700:], assign) {
			lo := idx - 350
			if lo < 0 {
				lo = 0
			}
			window := text[lo : idx+700]
			hasReturn := strings.Contains(window, "return nil") || strings.Contains(window, "return chatTurnFatal, nil")
			hasGate := strings.Contains(window, "codeChangedInRun(runStats)")
			if !hasReturn || !hasGate {
				t.Fatalf("early-return site %d: gate/return wiring incomplete (gate=%v return=%v) — the early-return verify bypass may be back", sites, hasGate, hasReturn)
			}
		}
		off = idx + len(assign)
	}
	if sites < 2 {
		t.Fatalf("expected >=2 asyncVerifyStats = runStats sites (strategist Complete guard + main path), found %d", sites)
	}
}
