package tool

import (
	"testing"
)

func registerTestTools(t *testing.T) *Registry {
	t.Helper()
	r := NewRegistry()
	err := RegisterBuiltinTools(r, nil, t.TempDir())
	if err != nil {
		t.Fatalf("RegisterBuiltinTools error: %v", err)
	}
	return r
}

func TestToolRegistry_Tools(t *testing.T) {
	r := registerTestTools(t)
	names := r.ToolNames()
	if len(names) == 0 {
		t.Error("expected at least one tool")
	}
}

func TestToolRegistry_ToolNames(t *testing.T) {
	r := registerTestTools(t)
	for _, name := range r.ToolNames() {
		if name == "" {
			t.Error("expected non-empty tool name")
		}
	}
}

func TestToolRegistry_ToDefinitions(t *testing.T) {
	r := registerTestTools(t)
	defs := r.ToDefinitions()
	if len(defs) == 0 {
		t.Error("expected at least one definition")
	}
	for _, def := range defs {
		if def.Name == "" {
			t.Error("expected non-empty name in definition")
		}
	}
}

func TestToolRegistry_Unregister(t *testing.T) {
	r := registerTestTools(t)
	before := len(r.ToolNames())
	r.Unregister("read_file")
	after := len(r.ToolNames())
	if after >= before {
		t.Error("expected fewer tools after unregister")
	}
}

func TestDomainFromURL_Tool(t *testing.T) {
	got := domainFromURL("https://example.com/path")
	if got == "" {
		t.Error("expected non-empty domain")
	}
}

func TestTodoFilePath_Fn(t *testing.T) {
	path := TodoFilePath("test-session-id")
	if path == "" {
		t.Error("expected non-empty path")
	}
}

func TestFormatCommandJobSnapshot_NilInput(t *testing.T) {
	got := formatCommandJobSnapshot(CommandJobSnapshot{}, false)
	_ = got
}
