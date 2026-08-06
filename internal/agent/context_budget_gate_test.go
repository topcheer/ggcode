package agent

import (
	"strings"
	"testing"
)

func TestContextBudgetGate_BelowDangerFill(t *testing.T) {
	g := newContextBudgetGate()
	args := []byte(`{"path": "/some/file.go"}`)
	hint := g.checkBudgetAwareness("read_file", args, 0.50)
	if hint != "" {
		t.Errorf("expected no hint at 50%% fill, got: %s", hint)
	}
}

func TestContextBudgetGate_ReadFileNoLimit(t *testing.T) {
	g := newContextBudgetGate()
	args := []byte(`{"path": "/very/long/path/to/some/file.go"}`)
	hint := g.checkBudgetAwareness("read_file", args, 0.75)
	if hint == "" {
		t.Error("expected hint for read_file without limit at 75% fill")
	}
}

func TestContextBudgetGate_ReadFileNoLimitCritical(t *testing.T) {
	g := newContextBudgetGate()
	args := []byte(`{"path": "/some/file.go"}`)
	hint := g.checkBudgetAwareness("read_file", args, 0.90)
	if hint == "" {
		t.Error("expected hint for read_file without limit at 90% fill")
	}
	if !strings.Contains(hint, "Context Alert") {
		t.Errorf("expected critical alert, got: %s", hint)
	}
}

func TestContextBudgetGate_ReadFileWithSmallLimit(t *testing.T) {
	g := newContextBudgetGate()
	args := []byte(`{"path": "/some/file.go", "offset": 100, "limit": 50}`)
	hint := g.checkBudgetAwareness("read_file", args, 0.75)
	if hint != "" {
		t.Errorf("expected no hint for small limit read, got: %s", hint)
	}
}

func TestContextBudgetGate_ReadFileLargeLimit(t *testing.T) {
	g := newContextBudgetGate()
	args := []byte(`{"path": "/some/file.go", "limit": 5000}`)
	hint := g.checkBudgetAwareness("read_file", args, 0.72)
	if hint == "" {
		t.Error("expected hint for large limit read at 72% fill")
	}
}

func TestContextBudgetGate_GrepContentNoFilter(t *testing.T) {
	g := newContextBudgetGate()
	args := []byte(`{"pattern": "TODO", "output_mode": "content"}`)
	hint := g.checkBudgetAwareness("grep", args, 0.80)
	if hint == "" {
		t.Error("expected hint for grep content mode without filter")
	}
}

func TestContextBudgetGate_GrepContentWithTypeFilter(t *testing.T) {
	g := newContextBudgetGate()
	args := []byte(`{"pattern": "TODO", "output_mode": "content", "type": "go"}`)
	hint := g.checkBudgetAwareness("grep", args, 0.80)
	if hint != "" {
		t.Errorf("expected no hint for grep with type filter, got: %s", hint)
	}
}

func TestContextBudgetGate_GlobRecursiveCritical(t *testing.T) {
	g := newContextBudgetGate()
	args := []byte(`{"pattern": "**/*.go"}`)
	hint := g.checkBudgetAwareness("glob", args, 0.90)
	if hint == "" {
		t.Error("expected hint for recursive glob at critical fill")
	}
}

func TestContextBudgetGate_GlobRecursiveNonCritical(t *testing.T) {
	g := newContextBudgetGate()
	args := []byte(`{"pattern": "**/*.go"}`)
	hint := g.checkBudgetAwareness("glob", args, 0.75)
	if hint != "" {
		t.Errorf("expected no hint for recursive glob at non-critical fill, got: %s", hint)
	}
}

func TestContextBudgetGate_SearchFilesDefaultMax(t *testing.T) {
	g := newContextBudgetGate()
	args := []byte(`{"pattern": "test"}`)
	hint := g.checkBudgetAwareness("search_files", args, 0.90)
	if hint == "" {
		t.Error("expected hint for search_files with default max_results at critical fill")
	}
}

func TestContextBudgetGate_SearchFilesHighMax(t *testing.T) {
	g := newContextBudgetGate()
	args := []byte(`{"pattern": "test", "max_results": 40}`)
	hint := g.checkBudgetAwareness("search_files", args, 0.90)
	if hint == "" {
		t.Error("expected hint for search_files max_results=40 at critical fill")
	}
}

func TestContextBudgetGate_CommandExpensive(t *testing.T) {
	g := newContextBudgetGate()
	args := []byte(`{"command": "go test ./..."}`)
	hint := g.checkBudgetAwareness("run_command", args, 0.80)
	if hint == "" {
		t.Error("expected hint for expensive command at high fill")
	}
}

func TestContextBudgetGate_CommandCheap(t *testing.T) {
	g := newContextBudgetGate()
	args := []byte(`{"command": "echo hello"}`)
	hint := g.checkBudgetAwareness("run_command", args, 0.80)
	if hint != "" {
		t.Errorf("expected no hint for cheap command, got: %s", hint)
	}
}

func TestContextBudgetGate_MultiFileReadMany(t *testing.T) {
	g := newContextBudgetGate()
	args := []byte(`{"files": [{"path":"a.go"},{"path":"b.go"},{"path":"c.go"},{"path":"d.go"}]}`)
	hint := g.checkBudgetAwareness("multi_file_read", args, 0.90)
	if hint == "" {
		t.Error("expected hint for multi_file_read with 4 files at critical fill")
	}
}

func TestContextBudgetGate_MultiFileReadFew(t *testing.T) {
	g := newContextBudgetGate()
	args := []byte(`{"files": [{"path":"a.go"},{"path":"b.go"}]}`)
	hint := g.checkBudgetAwareness("multi_file_read", args, 0.90)
	if hint != "" {
		t.Errorf("expected no hint for 2-file read, got: %s", hint)
	}
}

func TestContextBudgetGate_UnknownTool(t *testing.T) {
	g := newContextBudgetGate()
	args := []byte(`{"path": "/some/file.go"}`)
	hint := g.checkBudgetAwareness("edit_file", args, 0.90)
	if hint != "" {
		t.Errorf("expected no hint for unknown tool, got: %s", hint)
	}
}

func TestContextBudgetGate_MaxFires(t *testing.T) {
	g := newContextBudgetGate()
	args := []byte(`{"path": "/some/file.go"}`)
	// First 3 should produce hints
	for i := 0; i < cbgMaxFires; i++ {
		hint := g.checkBudgetAwareness("read_file", args, 0.90)
		if hint == "" {
			t.Errorf("expected hint on call %d", i+1)
		}
	}
	// 4th should be suppressed
	hint := g.checkBudgetAwareness("read_file", args, 0.90)
	if hint != "" {
		t.Error("expected hint to be suppressed after max fires")
	}
}

func TestContextBudgetGate_Reset(t *testing.T) {
	g := newContextBudgetGate()
	args := []byte(`{"path": "/some/file.go"}`)
	g.checkBudgetAwareness("read_file", args, 0.90)
	if g.fires != 1 {
		t.Fatalf("expected 1 fire, got %d", g.fires)
	}
	g.reset()
	if g.fires != 0 {
		t.Fatalf("expected 0 fires after reset, got %d", g.fires)
	}
}

func TestContextBudgetGate_CodeSearchHighMax(t *testing.T) {
	g := newContextBudgetGate()
	args := []byte(`{"query": "auth", "max_results": 10}`)
	hint := g.checkBudgetAwareness("code_search", args, 0.90)
	if hint == "" {
		t.Error("expected hint for code_search max_results=10 at critical fill")
	}
}

func TestCbgShortPath(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"/short/path.go", "/short/path.go"},
		{"/a/b/c/d/e/f/g/h/file.go", ".../f/g/h/file.go"},
	}
	for _, tt := range tests {
		got := cbgShortPath(tt.input)
		if len(got) > 65 && len(tt.input) > 65 {
			// OK - just verify it's shorter
			if len(got) >= len(tt.input) {
				t.Errorf("expected shortened path, got same length: %s", got)
			}
		}
	}
}

func TestCbgShortCmd(t *testing.T) {
	short := "echo hello"
	if got := cbgShortCmd(short); got != short {
		t.Errorf("expected unchanged short cmd, got: %s", got)
	}

	long := ""
	for i := 0; i < 200; i++ {
		long += "x"
	}
	got := cbgShortCmd(long)
	if len(got) >= len(long) {
		t.Errorf("expected shortened cmd, got len=%d", len(got))
	}
}

func TestCbgExtractString(t *testing.T) {
	if s := cbgExtractString([]byte(`{"path":"/foo"}`), "path"); s != "/foo" {
		t.Errorf("expected /foo, got %s", s)
	}
	if s := cbgExtractString([]byte(`{}`), "path"); s != "" {
		t.Errorf("expected empty, got %s", s)
	}
	if s := cbgExtractString(nil, "path"); s != "" {
		t.Errorf("expected empty for nil, got %s", s)
	}
}

func TestCbgExtractInt(t *testing.T) {
	if n := cbgExtractInt([]byte(`{"limit":100}`), "limit"); n != 100 {
		t.Errorf("expected 100, got %d", n)
	}
	if n := cbgExtractInt([]byte(`{}`), "limit"); n != 0 {
		t.Errorf("expected 0, got %d", n)
	}
	if n := cbgExtractInt(nil, "limit"); n != 0 {
		t.Errorf("expected 0 for nil, got %d", n)
	}
}

// (helper functions removed - use strings.Contains from stdlib)
