package tool

import (
	"context"
	"encoding/json"
	"testing"
)

func TestRunCommand_Basic(t *testing.T) {
	rc := RunCommand{WorkingDir: "/tmp"}
	input := json.RawMessage(`{"command": "echo hello"}`)
	result, err := rc.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content)
	}
	if result.Content != "hello\n" {
		t.Errorf("expected 'hello\\n', got %q", result.Content)
	}
}

func TestRunCommand_ExitCode(t *testing.T) {
	rc := RunCommand{WorkingDir: "/tmp"}
	input := json.RawMessage(`{"command": "exit 42"}`)
	result, err := rc.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for non-zero exit code")
	}
}

func TestRunCommand_Timeout(t *testing.T) {
	rc := RunCommand{WorkingDir: "/tmp"}
	input := json.RawMessage(`{"command": "sleep 60", "timeout": 1}`)
	result, err := rc.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for timeout")
	}
}

func TestRunCommand_InvalidJSON(t *testing.T) {
	rc := RunCommand{WorkingDir: "/tmp"}
	result, err := rc.Execute(context.Background(), json.RawMessage(`{invalid}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for invalid JSON")
	}
}

func TestRunCommand_Stderr(t *testing.T) {
	rc := RunCommand{WorkingDir: "/tmp"}
	input := json.RawMessage(`{"command": "echo error_msg >&2"}`)
	result, err := rc.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsStr(result.Content, "STDERR:") {
		t.Errorf("expected STDERR in output, got %q", result.Content)
	}
}

func TestRunCommand_WorkingDirIgnored(t *testing.T) {
	// The LLM-provided working_dir should be ignored; only struct field matters
	rc := RunCommand{WorkingDir: "/tmp"}
	input := json.RawMessage(`{"command": "pwd", "working_dir": "/nonexistent"}`)
	result, err := rc.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	// Should use /tmp, not /nonexistent
	if !containsStr(result.Content, "/tmp") {
		t.Errorf("expected /tmp in output, got %q", result.Content)
	}
}

func TestRunCommand_OutputTruncation(t *testing.T) {
	rc := RunCommand{WorkingDir: "/tmp"}
	// Generate output larger than 100KB
	input := json.RawMessage(`{"command": "python3 -c \"print('x'*200000)\" || yes x | head -n 200000"}`)
	result, err := rc.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Content) > 110000 {
		t.Errorf("expected truncated output, got length %d", len(result.Content))
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
