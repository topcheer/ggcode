package agent

import (
	"strings"
	"testing"
)

func Test3682ScopeNarrowExcludesStartCommand(t *testing.T) {
	// #5543: the scopeNarrow recording site moved out of agent.go; scan
	// the stream-loop source files (shape-agnostic file targeting).
	text, err := readStreamLoopSource("agent.go", "run_tool_call_idx.go")
	if err != nil {
		t.Fatalf("read stream-loop source: %v", err)
	}

	anchor := "a.scopeNarrow.recordVerificationCommand("
	idx := strings.Index(text, anchor)
	if idx < 0 {
		t.Fatal("scopeNarrow recording site not found in agent.go")
	}
	lo := idx - 500
	if lo < 0 {
		lo = 0
	}
	window := text[lo : idx+400]
	if !strings.Contains(window, "recordVerificationCommand") {
		t.Fatal("recordVerificationCommand call not at the scopeNarrow site")
	}

	guardAt := strings.Index(window, `if tc.Name == "run_command" {`)
	callAt := strings.Index(window, "recordVerificationCommand")
	if guardAt < 0 || callAt < 0 || guardAt > callAt {
		t.Fatalf("run_command-only guard missing or after the record call (guard=%d call=%d) — start_command may poison the narrowing baseline again (#3682 regression)", guardAt, callAt)
	}
}
