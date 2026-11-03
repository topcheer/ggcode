package agent

import (
	"testing"
)

func TestIssue2500CommandFailedWordBoundary(t *testing.T) {
	rs := &RunStats{
		CommandsRun: []string{"cargo build --release", "go test ./..."},
		Errors:      []string{"run_command: cargo build failed: error[E0308]: mismatched types"},
	}

	if commandFailed(rs, "go test ./...") {
		t.Fatalf("unrelated cargo failure (contains 'go' as substring) wrongly marked successful 'go test' as failed")
	}

	if !commandFailed(rs, "cargo build --release") {
		t.Fatalf("cargo failure no longer detected after word-boundary fix (regression of #1521 case D)")
	}
}

func TestIssue2500GoPathPollution(t *testing.T) {
	rs := &RunStats{
		CommandsRun: []string{"go build ./..."},
		Errors:      []string{"edit_file: failed to match old_text in internal/agent/foo.go"},
	}
	if commandFailed(rs, "go build ./...") {
		t.Fatalf("edit_file failure quoting a .go path wrongly marked successful 'go build' as failed")
	}
}

func TestIssue2500RealGoTestFailureStillDetected(t *testing.T) {
	rs := &RunStats{
		CommandsRun: []string{"go test ./..."},
		Errors:      []string{"run_command: go test exited with exit status 1: FAIL internal/agent"},
	}
	if !commandFailed(rs, "go test ./...") {
		t.Fatalf("genuine 'go test' failure no longer detected (word-boundary too strict)")
	}
}

func TestIssue2500HasVerificationCommandsEndToEnd(t *testing.T) {
	rs := &RunStats{
		CommandsRun: []string{"cargo build --release", "go test ./..."},
		Errors:      []string{"edit_file: failed to match old_text in internal/agent/foo.go"},
	}
	if !hasVerificationCommands(rs) {
		t.Fatalf("successful 'go test' not recognized as verification due to unrelated errors")
	}
}

func TestIssue2500UppercaseCommand(t *testing.T) {
	rs := &RunStats{
		CommandsRun: []string{"GO TEST ./..."},
		Errors:      []string{"run_command: go test failed with exit status 1"},
	}
	if !commandFailed(rs, "GO TEST ./...") {
		t.Fatalf("uppercase command anchor not lowercased before match")
	}
}
