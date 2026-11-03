package agent

import (
	"os"
	"strings"
	"testing"
)

func Test3682ScopeNarrowExcludesStartCommand(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatalf("read agent.go: %v", err)
	}
	text := string(src)

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
