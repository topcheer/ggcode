package agent

import (
	"encoding/json"
	"testing"
)

func issue3861Cmd(t *testing.T, tool, cmd string) (string, string) {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"command": cmd})
	return tool, string(b)
}

func TestIssue3861_LaunchDoesNotCreditCoverage(t *testing.T) {
	s := newEditCoverageState()
	s.recordToolCall("edit_file", issue3861CmdRaw(t, "/ws/internal/agent/foo.go"))
	s.recordToolCall("edit_file", issue3861CmdRaw(t, "/ws/internal/context/bar.go"))

	tool, args := issue3861Cmd(t, "start_command", "go test ./internal/agent/...")
	s.recordToolCall(tool, args)

	tool, args = issue3861Cmd(t, "run_command", "go test ./internal/context/")
	if warn := s.recordToolCall(tool, args); warn == "" {
		t.Fatal("background-launched (outcome unknown) package must stay UNVERIFIED on later commands")
	}
}

func TestIssue3861_CompletedRunStillCredits(t *testing.T) {
	s := newEditCoverageState()
	s.recordToolCall("edit_file", issue3861CmdRaw(t, "/ws/internal/agent/foo.go"))
	s.recordToolCall("edit_file", issue3861CmdRaw(t, "/ws/internal/context/bar.go"))

	tool, args := issue3861Cmd(t, "run_command", "go test ./internal/agent/...")
	if warn := s.recordToolCall(tool, args); warn == "" {
		t.Fatal("unverified context package must warn on the first covering-agent run")
	}
	tool, args = issue3861Cmd(t, "run_command", "go test ./internal/context/")
	if warn := s.recordToolCall(tool, args); warn != "" {
		t.Fatalf("both packages now verified - no warning expected, got: %s", warn)
	}
}

func TestIssue3861_EnvPrefixedVerifyRecognized(t *testing.T) {
	if !coverageIsVerifyCommand(`GOFLAGS="-p=1" go test ./...`) {
		t.Fatal("env-prefixed go test must be recognized as a verify command")
	}
	if !coverageIsVerifyCommand("GOFLAGS=-p=1 go build ./...") {
		t.Fatal("env-prefixed go build must be recognized")
	}
	if coverageIsVerifyCommand("GOFLAGS=-p=1 echo hi") {
		t.Fatal("non-verify command with env prefix must not match")
	}
}

func issue3861CmdRaw(t *testing.T, path string) string {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"file_path": path})
	return string(b)
}
