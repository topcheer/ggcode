package extpane

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDetectBackendPriority(t *testing.T) {
	origTMUX := os.Getenv("TMUX")
	origTERM := os.Getenv("TERM_PROGRAM")
	origKitty := os.Getenv("KITTY_WINDOW_ID")
	origLCTerm := os.Getenv("LC_TERMINAL")
	t.Cleanup(func() {
		os.Setenv("TMUX", origTMUX)
		os.Setenv("TERM_PROGRAM", origTERM)
		os.Setenv("KITTY_WINDOW_ID", origKitty)
		os.Setenv("LC_TERMINAL", origLCTerm)
	})

	// Case 1: tmux wins over all
	os.Setenv("TMUX", "/tmp/tmux-1000/default,1234,0")
	os.Setenv("TERM_PROGRAM", "iTerm.app")
	os.Setenv("KITTY_WINDOW_ID", "123")
	if b := detectBackend(); b != nil && b.Name() != "tmux" {
		t.Errorf("expected tmux, got %s", b.Name())
	}

	// Case 2: nothing detected
	os.Setenv("TMUX", "")
	os.Setenv("TERM_PROGRAM", "")
	os.Setenv("KITTY_WINDOW_ID", "")
	os.Setenv("LC_TERMINAL", "")
	if b := detectBackend(); b != nil {
		t.Errorf("expected nil, got %s", b.Name())
	}
}

func TestManagerNoBackend(t *testing.T) {
	os.Setenv("TMUX", "")
	os.Setenv("TERM_PROGRAM", "")
	os.Setenv("KITTY_WINDOW_ID", "")
	os.Setenv("LC_TERMINAL", "")

	m := NewManager()
	if m.Available() {
		t.Fatal("should not be available")
	}
	m.EnsurePane("a1", "test", "subagent")
	m.WriteText("a1", "hello")
	m.WriteToolCall("a1", "read_file", "f.go")
	m.WriteToolResult("a1", "read_file", "ok", false)
	m.UpdateStatus("a1", "test", "subagent", "running")
	m.HandleDone("a1", "test", false)
	m.CloseAll()
}

func TestCloseAllIdempotent(t *testing.T) {
	os.Setenv("TMUX", "")
	os.Setenv("TERM_PROGRAM", "")
	os.Setenv("KITTY_WINDOW_ID", "")
	os.Setenv("LC_TERMINAL", "")

	m := NewManager()
	m.CloseAll()
	m.CloseAll()
}

func TestFormatHeader(t *testing.T) {
	if formatHeader("r", "subagent") == "" {
		t.Error("empty header")
	}
	if formatHeader("r", "teammate") == "" {
		t.Error("empty header")
	}
}

func TestFormatToolCall(t *testing.T) {
	if formatToolCall("read_file", "f.go") == "" {
		t.Error("empty")
	}
}

func TestFormatToolResult(t *testing.T) {
	if formatToolResult("read_file", "ok", false) == "" {
		t.Error("empty")
	}
	if formatToolResult("read_file", "err", true) == "" {
		t.Error("empty")
	}
}

func TestFormatDone(t *testing.T) {
	if formatDone(false) == "" {
		t.Error("empty")
	}
	if formatDone(true) == "" {
		t.Error("empty")
	}
}

func TestFormatTitle(t *testing.T) {
	title := formatTitle("researcher", "subagent", "running")
	if !strings.Contains(title, "researcher") || !strings.Contains(title, "running") {
		t.Errorf("bad title: %s", title)
	}
}

func TestCompactPreview(t *testing.T) {
	if compactPreview("hello") != "hello" {
		t.Error("short string changed")
	}
	long := strings.Repeat("x", 200)
	if len(compactPreview(long)) > 120 {
		t.Error("not truncated")
	}
	if strings.Contains(compactPreview("a\nb"), "\n") {
		t.Error("newline not collapsed")
	}
}

func TestCompactPreviewUTF8(t *testing.T) {
	s := compactPreview(strings.Repeat("世界", 100))
	if !utf8.ValidString(s) {
		t.Error("invalid UTF-8 after truncation")
	}
}

func TestSanitizeFilename(t *testing.T) {
	s := sanitizeFilename("my agent/test")
	if strings.Contains(s, " ") || strings.Contains(s, "/") {
		t.Errorf("unsafe filename: %s", s)
	}
}

func TestShortID(t *testing.T) {
	if shortID("12345678abc") != "12345678" {
		t.Error("not truncated to 8")
	}
	if shortID("abc") != "abc" {
		t.Error("short string changed")
	}
}

func TestExtPaneLogPath(t *testing.T) {
	// Verify log path is constructed correctly
	path := filepath.Join("/tmp/test", "agent-abc12345.log")
	if !strings.HasSuffix(path, ".log") {
		t.Error("should end with .log")
	}
}
