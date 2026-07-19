//go:build darwin || linux

package agent

import (
	"context"
	"testing"
	"time"
)

func TestExecuteVerifyCommand_Success(t *testing.T) {
	a := &Agent{
		workingDir: ".",
	}
	result := a.executeVerifyCommand(context.Background(), "echo hello")
	if !result.Passed {
		t.Errorf("expected pass, got errors: %v", result.Errors)
	}
	if result.Command != "echo hello" {
		t.Errorf("expected command 'echo hello', got %q", result.Command)
	}
}

func TestExecuteVerifyCommand_Failure(t *testing.T) {
	a := &Agent{
		workingDir: ".",
	}
	result := a.executeVerifyCommand(context.Background(), "false")
	if result.Passed {
		t.Error("expected failure for 'false' command")
	}
	if len(result.Errors) == 0 {
		t.Error("expected at least one error line")
	}
}

func TestExecuteVerifyCommand_Timeout(t *testing.T) {
	a := &Agent{
		workingDir: ".",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	result := a.executeVerifyCommand(ctx, "sleep 30")
	if result.Passed {
		t.Error("expected failure due to timeout")
	}
}

func TestExecuteVerifyCommand_OutputTruncated(t *testing.T) {
	a := &Agent{
		workingDir: ".",
	}
	// Generate output longer than 2000 chars.
	result := a.executeVerifyCommand(context.Background(), "yes 'x' | head -5000")
	if !result.Passed {
		t.Errorf("expected pass, got errors: %v", result.Errors)
	}
	if len(result.Output) > 2100 { // 2000 + "..." slack
		t.Errorf("output not truncated: %d chars", len(result.Output))
	}
}

func TestVerifyExtractErrorLines(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect int
	}{
		{"empty", "", 0},
		{"no_errors", "all good\nlooks fine", 0},
		{"one_error", "line1\nError: something failed\nline3", 1},
		{"multiple_errors", "error one\nFAIL: second\npanic: third\nnormal line", 3},
		{"cap_at_10", func() string {
			s := ""
			for i := 0; i < 15; i++ {
				s += "error line\n"
			}
			return s
		}(), 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractErrorLines(tt.input)
			if len(got) != tt.expect {
				t.Errorf("expected %d errors, got %d: %v", tt.expect, len(got), got)
			}
		})
	}
}

func TestVerifyExtractErrorLines_DetectedPatterns(t *testing.T) {
	patterns := []string{
		"Error: undefined symbol",
		"FAIL: test case 3",
		"undefined: Foo in main.go",
		"cannot find package example.com/foo",
		"panic: runtime error",
		"FATAL: buffer overflow",
	}
	for _, p := range patterns {
		got := extractErrorLines(p)
		if len(got) != 1 {
			t.Errorf("pattern %q not detected, got %d matches", p, len(got))
		}
	}
}
