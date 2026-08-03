package agent

import (
	"strings"
	"testing"
)

func TestClassifyFailureMode(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		errorMsg string
		want     FailureMode
	}{
		// Systemic
		{"permission denied", "run_command", "bash: /usr/bin/foo: Permission denied", FailureModeSystemic},
		{"command not found", "run_command", "bash: foo: command not found", FailureModeSystemic},
		{"executable not found", "run_command", "exec: \"foo\": executable file not found", FailureModeSystemic},
		{"disk full", "write_file", "write: no space left on device", FailureModeSystemic},
		{"auth failure", "run_command", "fatal: unable to authenticate: invalid api key", FailureModeSystemic},
		{"quota exceeded", "run_command", "Error 429: quota exceeded", FailureModeSystemic},
		{"connection refused", "run_command", "dial tcp: connection refused", FailureModeSystemic},

		// Transient
		{"timeout", "run_command", "context deadline exceeded", FailureModeTransient},
		{"rate limit 429", "run_command", "HTTP 429: rate limit exceeded", FailureModeTransient},
		{"service unavailable", "run_command", "HTTP 503: service unavailable", FailureModeTransient},
		{"connection reset", "run_command", "read: connection reset by peer", FailureModeTransient},
		{"overloaded", "run_command", "Request overloaded, try again", FailureModeTransient},

		// Structural
		{"file not found (source)", "edit_file", "file not found: /path/to/foo.go", FailureModeStructural},
		{"type mismatch", "edit_file", "cannot use string as int value", FailureModeStructural},
		{"invalid argument", "grep", "invalid regex pattern", FailureModeStructural},
		{"wrong params", "edit_file", "old_text not found in file", FailureModeStructural},
		{"undefined reference", "run_command", "undefined: Foo in foo.go", FailureModeStructural},

		// Edge case: "no such file" for source file is structural
		{"no such file (source path)", "read_file", "open /foo/bar.go: no such file or directory", FailureModeStructural},
		// Edge case: "no such file" for binary is systemic
		{"no such file (binary)", "run_command", "exec: foo: no such file or directory", FailureModeSystemic},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyFailureMode(tt.toolName, tt.errorMsg)
			if got != tt.want {
				t.Errorf("classifyFailureMode(%q, %q) = %v, want %v", tt.toolName, tt.errorMsg, got, tt.want)
			}
		})
	}
}

func TestFailureModeStateSystemicFiresOnce(t *testing.T) {
	s := newFailureModeState()
	g1 := s.recordResult("run_command", true, "permission denied")
	if g1 == "" {
		t.Fatal("expected guidance on first systemic failure")
	}
	if !strings.Contains(g1, "SYSTEMIC") {
		t.Errorf("expected SYSTEMIC in guidance, got: %s", g1)
	}
	// Should not fire again
	g2 := s.recordResult("run_command", true, "access denied")
	if g2 != "" {
		t.Errorf("expected no guidance on second systemic failure, got: %s", g2)
	}
}

func TestFailureModeStateTransientFiresAfter3(t *testing.T) {
	s := newFailureModeState()
	// First two transient errors should not fire
	s.recordResult("run_command", true, "timeout")
	s.recordResult("run_command", true, "rate limit")
	// Third should fire
	g := s.recordResult("run_command", true, "503 service unavailable")
	if g == "" {
		t.Fatal("expected guidance after 3 transient failures")
	}
	if !strings.Contains(g, "TRANSIENT") {
		t.Errorf("expected TRANSIENT in guidance, got: %s", g)
	}
	// Should not fire again
	g2 := s.recordResult("run_command", true, "timeout")
	if g2 != "" {
		t.Errorf("expected no guidance on 4th transient failure, got: %s", g2)
	}
}

func TestFailureModeStateStructuralFiresAfter4(t *testing.T) {
	s := newFailureModeState()
	// First three structural errors should not fire
	s.recordResult("edit_file", true, "old_text not found")
	s.recordResult("edit_file", true, "old_text not found again")
	s.recordResult("grep", true, "invalid regex")
	// Fourth should fire
	g := s.recordResult("edit_file", true, "cannot use int as string")
	if g == "" {
		t.Fatal("expected guidance after 4 structural failures")
	}
	if !strings.Contains(g, "STRUCTURAL") {
		t.Errorf("expected STRUCTURAL in guidance, got: %s", g)
	}
}

func TestFailureModeStateNoErrorNoGuidance(t *testing.T) {
	s := newFailureModeState()
	g := s.recordResult("read_file", false, "file contents...")
	if g != "" {
		t.Errorf("expected no guidance on success, got: %s", g)
	}
}

func TestFailureModeStateReset(t *testing.T) {
	s := newFailureModeState()
	s.recordResult("run_command", true, "permission denied")
	s.recordResult("run_command", true, "permission denied")
	s.reset()
	// After reset, systemic should fire again
	g := s.recordResult("run_command", true, "auth failure")
	if g == "" {
		t.Fatal("expected guidance after reset")
	}
}

func TestFailureModeString(t *testing.T) {
	tests := []struct {
		mode FailureMode
		want string
	}{
		{FailureModeNone, "none"},
		{FailureModeTransient, "transient"},
		{FailureModeStructural, "structural"},
		{FailureModeSystemic, "systemic"},
	}
	for _, tt := range tests {
		if got := tt.mode.String(); got != tt.want {
			t.Errorf("FailureMode(%d).String() = %q, want %q", tt.mode, got, tt.want)
		}
	}
}
