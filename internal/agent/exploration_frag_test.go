package agent

import (
	"encoding/json"
	"testing"
)

func TestExplorationFrag_NoTriggerOnFewCalls(t *testing.T) {
	s := newExploreFragState()
	for i := 0; i < 3; i++ {
		args, _ := json.Marshal(map[string]string{"path": "/file" + string(rune('a'+i)) + ".go"})
		warn := s.recordToolCall("read_file", args, i+1)
		if warn != "" {
			t.Fatalf("expected no warning for %d calls, got: %s", i+1, warn)
		}
	}
}

func TestExplorationFrag_NoTriggerOnRepeatedSameFile(t *testing.T) {
	s := newExploreFragState()
	for i := 0; i < 8; i++ {
		args, _ := json.Marshal(map[string]string{"path": "/same/file.go"})
		warn := s.recordToolCall("read_file", args, i+1)
		if warn != "" {
			t.Fatalf("expected no warning for same-file reads, got: %s", warn)
		}
	}
}

func TestExplorationFrag_ResetOnMutatingTool(t *testing.T) {
	s := newExploreFragState()
	for i := 0; i < 5; i++ {
		args, _ := json.Marshal(map[string]string{"path": "/file" + string(rune('a'+i)) + ".go"})
		s.recordToolCall("read_file", args, i+1)
	}
	// Now do a mutating tool call -- should reset
	args, _ := json.Marshal(map[string]string{"file_path": "/filea.go"})
	warn := s.recordToolCall("edit_file", args, 6)
	if warn != "" {
		t.Fatalf("mutating tool should not trigger warning: %s", warn)
	}
	if len(s.entries) != 0 {
		t.Fatalf("expected entries cleared after mutating tool, got %d", len(s.entries))
	}
}

func TestExplorationFrag_TriggerOnScatteredCalls(t *testing.T) {
	s := newExploreFragState()
	files := []string{"/a.go", "/b.go", "/c.go", "/d.go", "/e.go", "/f.go"}
	var lastWarn string
	for i, f := range files {
		args, _ := json.Marshal(map[string]string{"path": f})
		lastWarn = s.recordToolCall("read_file", args, i+1)
	}
	if lastWarn == "" {
		t.Fatal("expected fragmentation warning after 6 scattered reads")
	}
}

func TestExplorationFrag_MaxWarnings(t *testing.T) {
	s := newExploreFragState()
	// First burst: should warn
	for i := 0; i < 6; i++ {
		args, _ := json.Marshal(map[string]string{"path": "/scatter" + string(rune('a'+i)) + ".go"})
		w := s.recordToolCall("read_file", args, i+1)
		if w != "" && s.warnings == 1 {
			break // first warning fired
		}
	}
	if s.warnings != 1 {
		t.Fatalf("expected 1 warning, got %d", s.warnings)
	}
	// Reset and trigger again
	s.reset()
	for i := 0; i < 6; i++ {
		args, _ := json.Marshal(map[string]string{"path": "/new" + string(rune('a'+i)) + ".go"})
		s.recordToolCall("read_file", args, i+10)
	}
	if s.warnings != 2 {
		t.Fatalf("expected 2 warnings, got %d", s.warnings)
	}
	// Third trigger should NOT warn (max reached)
	s.reset()
	warn := ""
	for i := 0; i < 6; i++ {
		args, _ := json.Marshal(map[string]string{"path": "/third" + string(rune('a'+i)) + ".go"})
		warn = s.recordToolCall("read_file", args, i+20)
	}
	if warn != "" {
		t.Fatal("expected no warning after max warnings reached")
	}
}

func TestExplorationFrag_MixedToolsTrigger(t *testing.T) {
	s := newExploreFragState()
	// Mix of different exploration tools across different targets
	calls := []struct {
		tool string
		args map[string]string
	}{
		{"read_file", map[string]string{"path": "/a.go"}},
		{"grep", map[string]string{"path": "/dir1", "pattern": "foo"}},
		{"glob", map[string]string{"pattern": "*.py"}},
		{"search_files", map[string]string{"directory": "/dir2", "pattern": "bar"}},
		{"read_file", map[string]string{"path": "/b.go"}},
		{"list_directory", map[string]string{"path": "/dir3"}},
	}
	var lastWarn string
	for i, c := range calls {
		args, _ := json.Marshal(c.args)
		lastWarn = s.recordToolCall(c.tool, args, i+1)
	}
	if lastWarn == "" {
		t.Fatal("expected fragmentation warning with mixed scattered tools")
	}
}

func TestExtractExploreJSONField(t *testing.T) {
	tests := []struct {
		json   string
		field  string
		expect string
	}{
		{`{"path":"/foo.go"}`, "path", "/foo.go"},
		{`{"pattern":"TODO"}`, "pattern", "TODO"},
		{`{"query":"auth"}`, "query", "auth"},
		{`{"other":"val"}`, "path", ""},
		{`{"path": "/spaced.go"}`, "path", "/spaced.go"},
	}
	for _, tt := range tests {
		got := extractExploreJSONField(tt.json, tt.field)
		if got != tt.expect {
			t.Errorf("extractExploreJSONField(%q, %q) = %q, want %q", tt.json, tt.field, got, tt.expect)
		}
	}
}

func TestExplorationFrag_IgnoresNonExplorationTools(t *testing.T) {
	s := newExploreFragState()
	args, _ := json.Marshal(map[string]string{"command": "echo hi"})
	warn := s.recordToolCall("im", args, 1)
	if warn != "" {
		t.Fatal("non-exploration tool should not produce warning")
	}
	if len(s.entries) != 0 {
		t.Fatalf("non-exploration tool should not be tracked, got %d entries", len(s.entries))
	}
}
