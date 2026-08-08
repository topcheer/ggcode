package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func rawMsg(t *testing.T, v map[string]interface{}) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSelfMod_ConfigFile(t *testing.T) {
	s := newSelfModState()
	args := rawMsg(t, map[string]interface{}{"path": "/project/ggcode.yaml"})
	msg := s.checkSelfModification("write_file", args)
	if msg == "" {
		t.Fatal("expected warning for ggcode.yaml")
	}
	if !strings.Contains(msg, "config") {
		t.Errorf("expected config category, got: %s", msg)
	}
	if !strings.Contains(msg, "HIGH") {
		t.Errorf("expected HIGH severity, got: %s", msg)
	}
}

func TestSelfMod_MemoryFile(t *testing.T) {
	s := newSelfModState()
	args := rawMsg(t, map[string]interface{}{"path": "/project/.ggcode/memory/patterns.md"})
	msg := s.checkSelfModification("edit_file", args)
	if msg == "" {
		t.Fatal("expected warning for memory file")
	}
	if !strings.Contains(msg, "memory") {
		t.Errorf("expected memory category, got: %s", msg)
	}
}

func TestSelfMod_SystemPrompt(t *testing.T) {
	s := newSelfModState()
	args := rawMsg(t, map[string]interface{}{"path": "/project/prompts/system_prompt.txt"})
	msg := s.checkSelfModification("write_file", args)
	if msg == "" {
		t.Fatal("expected warning for system prompt")
	}
	if !strings.Contains(msg, "CRITICAL") {
		t.Errorf("expected CRITICAL severity, got: %s", msg)
	}
}

func TestSelfMod_PermissionFile(t *testing.T) {
	s := newSelfModState()
	args := rawMsg(t, map[string]interface{}{"path": "/project/.ggcode/permissions/allowlist"})
	msg := s.checkSelfModification("write_file", args)
	if msg == "" {
		t.Fatal("expected warning for permission file")
	}
	if !strings.Contains(msg, "CRITICAL") {
		t.Errorf("expected CRITICAL severity, got: %s", msg)
	}
}

func TestSelfMod_HooksFile(t *testing.T) {
	s := newSelfModState()
	args := rawMsg(t, map[string]interface{}{"path": "/project/.ggcode/hooks/pre-commit.sh"})
	msg := s.checkSelfModification("write_file", args)
	if msg == "" {
		t.Fatal("expected warning for hooks file")
	}
	if !strings.Contains(msg, "hooks") {
		t.Errorf("expected hooks category, got: %s", msg)
	}
}

func TestSelfMod_BenignFile(t *testing.T) {
	s := newSelfModState()
	args := rawMsg(t, map[string]interface{}{"path": "/project/src/main.go"})
	msg := s.checkSelfModification("write_file", args)
	if msg != "" {
		t.Errorf("expected no warning for regular source file, got: %s", msg)
	}
}

func TestSelfMod_NonWriteTool(t *testing.T) {
	s := newSelfModState()
	args := rawMsg(t, map[string]interface{}{"path": "/project/ggcode.yaml"})
	msg := s.checkSelfModification("read_file", args)
	if msg != "" {
		t.Errorf("read_file should not trigger, got: %s", msg)
	}
}

func TestSelfMod_MultiFileEdit(t *testing.T) {
	s := newSelfModState()
	args := rawMsg(t, map[string]interface{}{
		"files": []interface{}{
			map[string]interface{}{"path": "/project/src/handler.go"},
			map[string]interface{}{"path": "/project/.ggcode/memory/test.md"},
		},
	})
	msg := s.checkSelfModification("multi_file_edit", args)
	if msg == "" {
		t.Fatal("expected warning for multi_file_edit with memory file")
	}
}

func TestSelfMod_MaxWarnings(t *testing.T) {
	s := newSelfModState()
	args1 := rawMsg(t, map[string]interface{}{"path": "/project/ggcode.yaml"})
	args2 := rawMsg(t, map[string]interface{}{"path": "/project/.ggcode/memory/a.md"})
	args3 := rawMsg(t, map[string]interface{}{"path": "/project/.ggcode/hooks/b.sh"})
	args4 := rawMsg(t, map[string]interface{}{"path": "/project/.ggcode/memory/c.md"})

	msg1 := s.checkSelfModification("write_file", args1)
	msg2 := s.checkSelfModification("write_file", args2)
	msg3 := s.checkSelfModification("write_file", args3)
	msg4 := s.checkSelfModification("write_file", args4)

	if msg1 == "" || msg2 == "" || msg3 == "" {
		t.Fatal("first 3 should warn")
	}
	if msg4 != "" {
		t.Error("4th should be suppressed after max warnings")
	}
}

func TestSelfMod_DedupPaths(t *testing.T) {
	s := newSelfModState()
	args := rawMsg(t, map[string]interface{}{"path": "/project/ggcode.yaml"})

	msg1 := s.checkSelfModification("write_file", args)
	if msg1 == "" {
		t.Fatal("first call should warn")
	}

	msg2 := s.checkSelfModification("write_file", args)
	if msg2 != "" {
		t.Error("same path should not warn again (dedup)")
	}
}

func TestSelfMod_Reset(t *testing.T) {
	s := newSelfModState()
	args := rawMsg(t, map[string]interface{}{"path": "/project/ggcode.yaml"})
	_ = s.checkSelfModification("write_file", args)
	if s.warningCount != 1 {
		t.Fatalf("expected warningCount=1, got %d", s.warningCount)
	}

	s.reset()
	if s.warningCount != 0 {
		t.Errorf("expected warningCount=0 after reset, got %d", s.warningCount)
	}
	if len(s.warnedPaths) != 0 {
		t.Errorf("expected warnedPaths empty after reset, got %d", len(s.warnedPaths))
	}
}

func TestSelfMod_BatchReplace(t *testing.T) {
	s := newSelfModState()
	args := rawMsg(t, map[string]interface{}{
		"files": []interface{}{"/project/ggcode.yaml", "/project/src/main.go"},
	})
	msg := s.checkSelfModification("batch_replace", args)
	if msg == "" {
		t.Fatal("expected warning for batch_replace with config file")
	}
}

func TestSelfMod_ProjectMemoryFiles(t *testing.T) {
	cases := []string{
		"/project/AGENTS.md",
		"/project/GGCODE.md",
		"/project/CLAUDE.md",
		"/project/COPILOT.md",
	}
	for _, p := range cases {
		s := newSelfModState()
		args := rawMsg(t, map[string]interface{}{"path": p})
		msg := s.checkSelfModification("write_file", args)
		if msg == "" {
			t.Errorf("expected warning for %s", p)
		}
	}
}

func TestSelfMod_BenignPathsWithSimilarNames(t *testing.T) {
	cases := []string{
		"/project/src/config.go",
		"/project/docs/hooks-guide.md",
		"/project/test/permission_test.go",
	}
	for _, p := range cases {
		s := newSelfModState()
		args := rawMsg(t, map[string]interface{}{"path": p})
		msg := s.checkSelfModification("write_file", args)
		if msg != "" {
			t.Errorf("expected no warning for %s, got: %s", p, msg)
		}
	}
}
