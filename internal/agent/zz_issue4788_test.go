package agent

import (
	"encoding/json"
	"testing"
)

func TestIssue4788_FailedBuildRetryNotRedundant(t *testing.T) {
	s := newBuildIdempotencyState()
	buildArgs, _ := json.Marshal(map[string]string{"command": "go build ./..."})

	if w := s.recordToolCall("run_command", buildArgs, 1); w != "" {
		t.Fatalf("first build should not warn, got %q", w)
	}

	s.markBuildFailed("go build ./...")

	if w := s.recordToolCall("run_command", buildArgs, 2); w != "" {
		t.Fatalf("retry after a failed run must not be flagged redundant, got %q", w)
	}
}

func TestIssue4788_SuccessfulBuildRetryStillWarns(t *testing.T) {
	s := newBuildIdempotencyState()
	buildArgs, _ := json.Marshal(map[string]string{"command": "go build ./..."})

	s.recordToolCall("run_command", buildArgs, 1)
	if w := s.recordToolCall("run_command", buildArgs, 2); w == "" {
		t.Fatal("zero-edit re-run after a successful build must still warn (idempotency contract)")
	}
}

func TestIssue4788_MarkFailedIsKeyScoped(t *testing.T) {
	s := newBuildIdempotencyState()
	aArgs, _ := json.Marshal(map[string]string{"command": "go build ./internal/agent/"})
	s.recordToolCall("run_command", aArgs, 1)

	s.markBuildFailed("go build ./internal/tool/")

	bArgs, _ := json.Marshal(map[string]string{"command": "go build ./internal/agent/"})
	if w := s.recordToolCall("run_command", bArgs, 2); w == "" {
		t.Fatal("failure of a different command must not clear this anchor")
	}
}
