package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRunCommandDescriptionClarifiesQuickVsBackground(t *testing.T) {
	tool := RunCommand{}
	for _, want := range []string{"quick one-shot", "prefer start_command", "interactive commands"} {
		if !containsAny(tool.Description(), want) {
			t.Fatalf("run_command description should mention %q, got %q", want, tool.Description())
		}
	}
	params := string(tool.Parameters())
	for _, want := range []string{"quick one-shot commands", "prefer start_command", "# Run tests"} {
		if !containsAny(params, want) {
			t.Fatalf("run_command schema should mention %q, got %s", want, params)
		}
	}
}

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

func TestRunCommand_ContextCancelStopsShellProcessGroup(t *testing.T) {
	rc := RunCommand{WorkingDir: "/tmp"}
	input := json.RawMessage(`{"command": "sleep 60 | cat"}`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan Result, 1)
	go func() {
		result, _ := rc.Execute(ctx, input)
		done <- result
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case result := <-done:
		if !result.IsError {
			t.Fatalf("expected canceled command to report an error result")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected canceled command to stop promptly")
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

func TestTruncateMiddle_ShortStringUnchanged(t *testing.T) {
	s := "hello world"
	got := truncateMiddle(s, 100, "output")
	if got != s {
		t.Fatalf("short string should be unchanged, got: %s", got)
	}
}

func TestTruncateMiddle_PreservesHeadAndTail(t *testing.T) {
	// Create a 10000-byte string: "HEAD_START" + padding + "TAIL_END"
	var sb strings.Builder
	sb.WriteString("HEAD_START")
	for i := 0; i < 9980; i++ {
		sb.WriteString("x")
	}
	sb.WriteString("TAIL_END")
	s := sb.String()
	if len(s) < 1001 {
		t.Fatalf("setup error: expected >1000 bytes, got %d", len(s))
	}

	got := truncateMiddle(s, 1000, "output")
	if !containsStr(got, "HEAD_START") {
		t.Error("truncated output should contain head")
	}
	if !containsStr(got, "TAIL_END") {
		t.Error("truncated output should contain tail — this is the key fix")
	}
	if !containsStr(got, "bytes omitted") {
		t.Error("truncated output should contain omission marker")
	}
}

func TestTruncateMiddle_ExactSize(t *testing.T) {
	s := strings.Repeat("a", 100)
	got := truncateMiddle(s, 100, "output")
	if got != s {
		t.Fatal("string at exactly maxLen should not be truncated")
	}
}
