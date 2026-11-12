package agent

import (
	"strings"
	"testing"
)

func Test3683EarlyReturnsSetAsyncVerifyStats(t *testing.T) {
	// #5543: the early-return paths moved out of agent.go; scan the
	// stream-loop source files (shape-agnostic file targeting).
	text, err := readStreamLoopSource("agent.go", "run_no_tool_calls.go")
	if err != nil {
		t.Fatalf("read stream-loop source: %v", err)
	}

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
		// #5543: the extracted method signals termination via
		// noToolCallsTerminal instead of a bare `return nil`; accept both
		// shapes (the main loop turns the terminal signal into the run's
		// final nil error).
		hasReturn := strings.Contains(window, "return nil") ||
			strings.Contains(window, "return noToolCallsTerminal")
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
