package agent

import (
	"os"
	"strings"
	"testing"
)

// #3989: recordSuccessfulCommand used to record the inner command of ANY
// non-error result, including start_command - whose IsError=false only
// proves the background job spawned, not that `go test ./...` passed. The
// entry is never removed by wait_command, and the post-run distiller
// persists it as a "verified-successful" cmd_snippet across sessions: the
// exact same unverified-signal poisoning #3682 excluded one if-block above.
// This probe pins the wiring: recordSuccessfulCommand must sit under a
// run_command-only guard at the distillation site.
func Test3989SuccessfulCommandExcludesStartCommand(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatalf("read agent.go: %v", err)
	}
	text := string(src)

	anchor := "Trajectory\u2192asset distillation"
	idx := strings.Index(text, anchor)
	if idx < 0 {
		t.Fatal("distillation recording site not found in agent.go")
	}
	window := text[idx : idx+1100]
	if !strings.Contains(window, "recordSuccessfulCommand") {
		t.Fatal("recordSuccessfulCommand call not at the distillation site")
	}
	// The guard must gate the record call with run_command-only.
	guardAt := strings.Index(window, `if tc.Name == "run_command" && !result.IsError {`)
	callAt := strings.Index(window, "recordSuccessfulCommand")
	if guardAt < 0 || callAt < 0 || guardAt > callAt {
		t.Fatalf("run_command-only guard missing or after the record call (guard=%d call=%d) - a spawned-but-failing background job would be distilled as a verified-successful snippet again (#3989 regression)", guardAt, callAt)
	}
	if !strings.Contains(window, "#3989") {
		t.Fatal("#3989 rationale comment missing at the site")
	}
}
