package agent

import (
	"encoding/json"
	"testing"
)

// #4788 (S-18): the idempotency hook records lastBuildCmd at tool-CALL time
// (no exit status), so a FAILED build still became the anchor - and the
// retry, with 0 edits in between, was misjudged "redundant / guaranteed
// identical" about a run that never succeeded. markBuildFailed (wired from
// recordEffectAttempt on effectFailed) clears the anchor so the retry is
// judged on its own outcome.
func TestIssue4788_FailedBuildRetryNotRedundant(t *testing.T) {
	s := newBuildIdempotencyState()
	buildArgs, _ := json.Marshal(map[string]string{"command": "go build ./..."})

	if w := s.recordToolCall("run_command", buildArgs, 1); w != "" {
		t.Fatalf("first build should not warn, got %q", w)
	}
	// The build FAILED (exit != 0): the post-execution outcome hook fires.
	s.markBuildFailed("go build ./...")

	// Retry with zero edits in between must NOT be called redundant.
	if w := s.recordToolCall("run_command", buildArgs, 2); w != "" {
		t.Fatalf("retry after a failed run must not be flagged redundant, got %q", w)
	}
}

// Control: a SUCCESSFUL build (no failure hook) keeps the anchor, and the
// zero-edit re-run still warns - the original idempotency contract.
func TestIssue4788_SuccessfulBuildRetryStillWarns(t *testing.T) {
	s := newBuildIdempotencyState()
	buildArgs, _ := json.Marshal(map[string]string{"command": "go build ./..."})

	s.recordToolCall("run_command", buildArgs, 1)
	if w := s.recordToolCall("run_command", buildArgs, 2); w == "" {
		t.Fatal("zero-edit re-run after a successful build must still warn (idempotency contract)")
	}
}

// markBuildFailed must only clear the anchor for the exact failed command,
// not for a different build command that happens to share the label.
func TestIssue4788_MarkFailedIsKeyScoped(t *testing.T) {
	s := newBuildIdempotencyState()
	aArgs, _ := json.Marshal(map[string]string{"command": "go build ./internal/agent/"})
	s.recordToolCall("run_command", aArgs, 1)

	// A DIFFERENT command failed; the ./internal/agent/ anchor survives.
	s.markBuildFailed("go build ./internal/tool/")

	bArgs, _ := json.Marshal(map[string]string{"command": "go build ./internal/agent/"})
	if w := s.recordToolCall("run_command", bArgs, 2); w == "" {
		t.Fatal("failure of a different command must not clear this anchor")
	}
}
