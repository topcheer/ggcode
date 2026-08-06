package agent

import (
	"encoding/json"
	"testing"
)

func TestSilentError_Reset(t *testing.T) {
	s := newSilentErrorState()
	s.recordToolError("edit_file", "/foo.go", "error", 1)
	s.recordToolAction("run_command", "go build")
	s.fired = true
	s.reset()

	if len(s.unresolvedErrors) != 0 {
		t.Errorf("expected unresolvedErrors cleared, got %d", len(s.unresolvedErrors))
	}
	if s.silentAdvancementCount != 0 {
		t.Errorf("expected count 0, got %d", s.silentAdvancementCount)
	}
	if s.fired {
		t.Error("expected fired=false")
	}
}

func TestSilentError_RecordsError(t *testing.T) {
	s := newSilentErrorState()
	s.recordToolError("edit_file", "/foo.go", "line not found", 1)

	if len(s.unresolvedErrors) != 1 {
		t.Fatalf("expected 1 unresolved error, got %d", len(s.unresolvedErrors))
	}
	if s.unresolvedErrors[0].toolName != "edit_file" {
		t.Errorf("expected edit_file, got %s", s.unresolvedErrors[0].toolName)
	}
}

func TestSilentError_ActionAddressesSameResource(t *testing.T) {
	s := newSilentErrorState()
	s.recordToolError("edit_file", "/foo.go", "error", 1)

	// Same resource = addresses the error, should not count as silent advancement.
	msg := s.recordToolAction("edit_file", "/foo.go")
	if msg != "" {
		t.Errorf("expected no guidance, got %s", msg)
	}
	if s.silentAdvancementCount != 0 {
		t.Errorf("expected 0 advancements, got %d", s.silentAdvancementCount)
	}
}

func TestSilentError_DifferentResourceIsAdvancement(t *testing.T) {
	s := newSilentErrorState()
	s.recordToolError("edit_file", "/foo.go", "error", 1)

	// Different resource = silent advancement.
	msg := s.recordToolAction("edit_file", "/bar.go")
	if msg != "" {
		t.Errorf("expected no guidance at count=1, got %s", msg)
	}
	if s.silentAdvancementCount != 1 {
		t.Errorf("expected 1 advancement, got %d", s.silentAdvancementCount)
	}
}

func TestSilentError_FiresAfterThreshold(t *testing.T) {
	s := newSilentErrorState()

	// Error 1, then advance.
	s.recordToolError("edit_file", "/foo.go", "error 1", 1)
	s.recordToolAction("run_command", "ls")

	// Error 2, then advance.
	s.recordToolError("edit_file", "/bar.go", "error 2", 2)
	s.recordToolAction("run_command", "pwd")

	// Error 3, then advance - should fire.
	s.recordToolError("edit_file", "/baz.go", "error 3", 3)
	msg := s.recordToolAction("run_command", "date")

	if msg == "" {
		t.Error("expected guidance after 3 silent advancements")
	}
	if !s.fired {
		t.Error("expected fired=true")
	}
}

func TestSilentError_DoesNotFireWhenAddressingErrors(t *testing.T) {
	s := newSilentErrorState()

	// Error 1, then address it.
	s.recordToolError("edit_file", "/foo.go", "error 1", 1)
	s.recordToolAction("edit_file", "/foo.go")

	// Error 2, then address it.
	s.recordToolError("run_command", "go build", "build error", 2)
	s.recordToolAction("run_command", "go build")

	// Error 3, then address it.
	s.recordToolError("edit_file", "/bar.go", "error 3", 3)
	msg := s.recordToolAction("read_file", "/bar.go")

	if msg != "" {
		t.Errorf("expected no guidance when errors addressed, got %s", msg)
	}
}

func TestSilentError_PrefixMatchAddressesError(t *testing.T) {
	s := newSilentErrorState()
	// Error on a build command.
	s.recordToolError("run_command", "go build ./...", "build error", 1)

	// Agent runs a targeted build on a subpackage - should count as addressing.
	// "go build ./pkg/..." has prefix match with "go build".
	msg := s.recordToolAction("run_command", "go build ./pkg/")
	if msg != "" {
		t.Errorf("expected no guidance for prefix match, got %s", msg)
	}
}

func TestSilentError_FiresOnce(t *testing.T) {
	s := newSilentErrorState()
	s.fired = true

	s.recordToolError("edit_file", "/foo.go", "error", 1)
	msg := s.recordToolAction("run_command", "ls")
	if msg != "" {
		t.Errorf("expected no guidance after already fired, got %s", msg)
	}
}

func TestSilentError_MaxTracked(t *testing.T) {
	s := newSilentErrorState()
	for i := 0; i < silentErrorMaxTracked+5; i++ {
		s.recordToolError("edit_file", "/foo.go", "error", i)
	}
	if len(s.unresolvedErrors) > silentErrorMaxTracked {
		t.Errorf("expected at most %d errors, got %d", silentErrorMaxTracked, len(s.unresolvedErrors))
	}
}

func TestSilentError_EmptyResourceKeyNotTracked(t *testing.T) {
	s := newSilentErrorState()
	// Some tools don't produce resource keys.
	s.recordToolError("lsp_hover", "", "error", 1)
	// Empty resource key can't be matched, so nothing should fire.
	msg := s.recordToolAction("run_command", "ls")
	// With no resource keys to match against, every action is a silent advancement,
	// but we need 3 to fire. Let's add more errors.
	if msg != "" {
		t.Errorf("expected no guidance at count 1, got: %s", msg)
	}
}

func TestSilentError_NormalizeResourceKey(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"  /foo/bar.go  ", "/foo/bar.go"},
		{"go build ./...", "go build"},
		{"npm test", "npm test"},
		{"cargo build --release", "cargo build"},
		{"python script.py", "python script.py"},
		{"python3 script.py", "python3 script.py"},
		{"Makefile", "makefile"},
	}
	for _, tt := range tests {
		got := normalizeResourceKey(tt.input)
		if got != tt.expected {
			t.Errorf("normalizeResourceKey(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestSilentError_ExtractErrorResourceKey(t *testing.T) {
	args, _ := json.Marshal(map[string]interface{}{
		"file_path": "/path/to/file.go",
	})
	key := extractErrorResourceKey("edit_file", args)
	if key != "/path/to/file.go" {
		t.Errorf("expected /path/to/file.go, got %s", key)
	}

	cmdArgs, _ := json.Marshal(map[string]interface{}{
		"command": "go build ./...",
	})
	cmdKey := extractErrorResourceKey("run_command", cmdArgs)
	if cmdKey != "go build" {
		t.Errorf("expected 'go build', got %s", cmdKey)
	}
}

func TestSilentError_BuildGuidance(t *testing.T) {
	s := newSilentErrorState()
	s.recordToolError("edit_file", "/foo.go", "line not found", 1)
	s.recordToolError("run_command", "go build", "compilation failed", 2)
	s.recordToolError("edit_file", "/bar.go", "anchor mismatch", 3)

	// Trigger 3 silent advancements to hit threshold.
	msg := ""
	for i := 0; i < silentErrorThreshold; i++ {
		msg = s.recordToolAction("read_file", "/unrelated.go")
	}
	if msg == "" {
		t.Error("expected non-empty guidance after threshold")
	}
	if !s.fired {
		t.Error("expected fired=true")
	}
}
