package agent

import (
	"testing"
)

// TestIssue2500CommandFailedWordBoundary pins the #2500 fix: commandFailed
// must use word-bounded anchor matching. The bare strings.Contains matched
// "go" inside "cargo" and inside ".go" file paths, so a single unrelated
// tool error invalidated the verification exemption for a SUCCESSFUL
// "go test" run (false-positive unverified-claim reminders).
func TestIssue2500CommandFailedWordBoundary(t *testing.T) {
	rs := &RunStats{
		CommandsRun: []string{"cargo build --release", "go test ./..."},
		Errors:      []string{"run_command: cargo build failed: error[E0308]: mismatched types"},
	}
	// The failed cargo build must not mark the successful "go test" as failed.
	if commandFailed(rs, "go test ./...") {
		t.Fatalf("unrelated cargo failure (contains 'go' as substring) wrongly marked successful 'go test' as failed")
	}
	// And the cargo command itself is still correctly detected as failed.
	if !commandFailed(rs, "cargo build --release") {
		t.Fatalf("cargo failure no longer detected after word-boundary fix (regression of #1521 case D)")
	}
}

// TestIssue2500GoPathPollution covers the near-universal Go-repo trigger:
// an edit_file error quoting a *.go path contains "go" + "failed" and used
// to invalidate every go build/test exemption in the run.
func TestIssue2500GoPathPollution(t *testing.T) {
	rs := &RunStats{
		CommandsRun: []string{"go build ./..."},
		Errors:      []string{"edit_file: failed to match old_text in internal/agent/foo.go"},
	}
	if commandFailed(rs, "go build ./...") {
		t.Fatalf("edit_file failure quoting a .go path wrongly marked successful 'go build' as failed")
	}
}

// TestIssue2500RealGoTestFailureStillDetected guards the #1521 case D intent:
// an error line that genuinely reports "go test" failing still counts.
func TestIssue2500RealGoTestFailureStillDetected(t *testing.T) {
	rs := &RunStats{
		CommandsRun: []string{"go test ./..."},
		Errors:      []string{"run_command: go test exited with exit status 1: FAIL internal/agent"},
	}
	if !commandFailed(rs, "go test ./...") {
		t.Fatalf("genuine 'go test' failure no longer detected (word-boundary too strict)")
	}
}

// TestIssue2500HasVerificationCommandsEndToEnd verifies the consumer path:
// with a successful go test and only unrelated errors present, the
// verification exemption must hold (no unverified-claim fire).
func TestIssue2500HasVerificationCommandsEndToEnd(t *testing.T) {
	rs := &RunStats{
		CommandsRun: []string{"cargo build --release", "go test ./..."},
		Errors:      []string{"edit_file: failed to match old_text in internal/agent/foo.go"},
	}
	if !hasVerificationCommands(rs) {
		t.Fatalf("successful 'go test' not recognized as verification due to unrelated errors")
	}
}

// TestIssue2500UppercaseCommand covers anchor lowercase normalization.
func TestIssue2500UppercaseCommand(t *testing.T) {
	rs := &RunStats{
		CommandsRun: []string{"GO TEST ./..."},
		Errors:      []string{"run_command: go test failed with exit status 1"},
	}
	if !commandFailed(rs, "GO TEST ./...") {
		t.Fatalf("uppercase command anchor not lowercased before match")
	}
}
