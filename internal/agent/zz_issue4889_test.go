package agent

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// #4889: start_command records the build-idempotency anchor at launch; the
// failure only surfaces later through wait_command/read_command_output.
// The clearing seam needs the job's ORIGINAL command keyed by job_id
// (psJobCommand) and the terminal-failure classification to combine into a
// markBuildFailed call.
func TestIssue4889_BackgroundBuildFailureClearsAnchor(t *testing.T) {
	p := newPrematureSuccessState()
	p.mu.Lock()
	p.psRegisterJobLocked("job-1", "go build ./...")
	p.mu.Unlock()

	cmd, ok := p.psJobCommand("job-1")
	if !ok || cmd != "go build ./..." {
		t.Fatalf("psJobCommand must return the launch command: got %q ok=%v", cmd, ok)
	}

	// Simulate the terminal observation a wait_command result carries.
	failedTerminal := "Status: failed\nExit: 1\ninternal/tool/x.go:42: undefined: y"
	if terminal, passed := psTerminalVerifyOutcome(psParseJobStatus(failedTerminal)); !terminal || passed {
		t.Fatalf("exit-1 terminal output must classify as terminal failure: terminal=%v passed=%v", terminal, passed)
	}

	// Wire the two together exactly as the agent.go seam does.
	s := newBuildIdempotencyState()
	s.recordToolCall("start_command", mustJSON4889(t, "go build ./..."), 1)
	w1 := s.recordToolCall("run_command", mustJSON4889(t, "go build ./..."), 2)
	if !strings.Contains(w1, "0 source edits") {
		t.Fatalf("pre-fix behavior sanity: same anchor + 0 edits should warn, got %q", w1)
	}

	s2 := newBuildIdempotencyState()
	s2.recordToolCall("start_command", mustJSON4889(t, "go build ./..."), 1)
	if terminal, passed := psTerminalVerifyOutcome(psParseJobStatus(failedTerminal)); terminal && !passed {
		s2.markBuildFailed(cmd)
	}
	w2 := s2.recordToolCall("run_command", mustJSON4889(t, "go build ./..."), 2)
	if strings.Contains(w2, "0 source edits") {
		t.Fatalf("after the background failure clears the anchor, the retry must not be mislabeled: %q", w2)
	}
}

func mustJSON4889(t *testing.T, command string) json.RawMessage {
	t.Helper()
	return json.RawMessage(`{"command":` + strconv.Quote(command) + `}`)
}
