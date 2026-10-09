package agent

import (
	"os"
	"strings"
	"testing"
)

// #3682: the scopeNarrow recording gate used to feed BOTH run_command and
// start_command into recordVerificationCommand. A start_command exit status
// only reflects "job started", so a background `start_command "go test ./..."`
// was recorded as a PASSED verification, poisoning the narrowing history
// baseline (correctionSpiral already excluded start_command for the same
// reason). This probe pins the wiring: recordVerificationCommand must sit
// under a run_command-only gate at the scopeNarrow site.
func Test3682ScopeNarrowExcludesStartCommand(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatalf("read agent.go: %v", err)
	}
	text := string(src)

	anchor := "Verification scope narrowing: detect progressively narrowing"
	idx := strings.Index(text, anchor)
	if idx < 0 {
		t.Fatal("scopeNarrow recording site not found in agent.go")
	}
	window := text[idx : idx+900]
	if !strings.Contains(window, "recordVerificationCommand") {
		t.Fatal("recordVerificationCommand call not at the scopeNarrow site")
	}
	// The guard must appear BEFORE the record call in this window.
	guardAt := strings.Index(window, `if tc.Name == "run_command" {`)
	callAt := strings.Index(window, "recordVerificationCommand")
	if guardAt < 0 || callAt < 0 || guardAt > callAt {
		t.Fatalf("run_command-only guard missing or after the record call (guard=%d call=%d) — start_command may poison the narrowing baseline again (#3682 regression)", guardAt, callAt)
	}
	if !strings.Contains(window, "#3682") {
		t.Fatal("#3682 rationale comment missing at the site")
	}
}
