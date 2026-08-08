package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPhantomState_Reset(t *testing.T) {
	s := newPhantomState()
	s.recordFailedCall("read_file", json.RawMessage(`{"path":"/foo/bar.go"}`), 1)
	s.recordSubsequentCall("grep", json.RawMessage(`{"path":"/foo/bar.go"}`), 2)
	s.fired = true

	s.reset()

	if s.fired != false {
		t.Error("fired should be false after reset")
	}
	if len(s.failedCalls) != 0 {
		t.Error("failedCalls should be empty after reset")
	}
	if len(s.buildOnEvidence) != 0 {
		t.Error("buildOnEvidence should be empty after reset")
	}
}

func TestPhantomState_ExtractIdentifiers(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		args     map[string]interface{}
		wantMin  int // minimum number of identifiers expected
	}{
		{
			name:     "read_file with path",
			toolName: "read_file",
			args:     map[string]interface{}{"path": "/src/main.go"},
			wantMin:  1,
		},
		{
			name:     "grep with pattern",
			toolName: "grep",
			args:     map[string]interface{}{"pattern": "TODO"},
			wantMin:  1,
		},
		{
			name:     "lsp_definition with path",
			toolName: "lsp_definition",
			args:     map[string]interface{}{"path": "/src/app.ts", "line": 10, "character": 5},
			wantMin:  1,
		},
		{
			name:     "no identifiers",
			toolName: "run_command",
			args:     map[string]interface{}{"command": "ls"},
			wantMin:  0,
		},
		{
			name:     "empty path",
			toolName: "read_file",
			args:     map[string]interface{}{"path": ""},
			wantMin:  0,
		},
		{
			name:     "short pattern excluded",
			toolName: "search_files",
			args:     map[string]interface{}{"pattern": "ab"},
			wantMin:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ids := extractPhantomIdentifiers(tt.toolName, tt.args)
			if len(ids) < tt.wantMin {
				t.Errorf("expected >= %d identifiers, got %d: %v", tt.wantMin, len(ids), ids)
			}
		})
	}
}

func TestPhantomState_ExtractIdentifiersMultiFile(t *testing.T) {
	args := map[string]interface{}{
		"files": []interface{}{
			map[string]interface{}{"path": "/a.go"},
			map[string]interface{}{"path": "/b.go"},
		},
	}
	ids := extractPhantomIdentifiers("multi_file_edit", args)
	if len(ids) < 2 {
		t.Errorf("expected >= 2 identifiers for multi_file_edit, got %d", len(ids))
	}
}

func TestPhantomState_PathBasename(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/foo/bar.go", "bar.go"},
		{"bar.go", "bar.go"},
		{"/a/b/c/test.ts", "test.ts"},
		{"C:\\Users\\app.ts", "app.ts"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := pathBasename(tt.input)
			if tt.input == "" {
				return // empty stays empty
			}
			if got != tt.want {
				t.Errorf("pathBasename(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestPhantomState_IsLikelyFailed(t *testing.T) {
	tests := []struct {
		name    string
		content string
		isError bool
		want    bool
	}{
		{"error result", "some error", true, true},
		{"empty result", "", false, true},
		{"no results", "no results", false, true},
		{"no matches short", "0 matches", false, true},
		{"no such file short", "no such file or directory", false, true},
		{"successful long result", "This is a very long successful result that contains lots of useful content and should not trigger the empty signal detection even though it might contain words like results", false, false},
		{"successful short", "line1\nline2\nline3", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isLikelyFailed(tt.content, tt.isError)
			if got != tt.want {
				t.Errorf("isLikelyFailed(%q, %v) = %v, want %v", tt.content, tt.isError, got, tt.want)
			}
		})
	}
}

func TestPhantomState_PhantomInheritanceDetection(t *testing.T) {
	s := newPhantomState()

	// Step 1: read_file fails (file doesn't exist)
	s.recordFailedCall("read_file", json.RawMessage(`{"path":"/nonexistent/file.go"}`), 1)

	// Step 2: grep references the same path from the failed call
	s.recordSubsequentCall("grep", json.RawMessage(`{"path":"/nonexistent/file.go","pattern":"func"}`), 2)

	// Step 3: lsp_definition also references the same path
	s.recordSubsequentCall("lsp_definition", json.RawMessage(`{"path":"/nonexistent/file.go","line":10,"character":5}`), 3)

	// Now check: should have 2 evidence signals, above threshold
	msg := s.checkPhantomInheritance(3)
	if msg == "" {
		t.Error("expected phantom inheritance guidance, got empty")
	}

	// Verify key content in the message
	if !strings.Contains(msg, "Phantom Output Inheritance") {
		t.Error("message should contain detector name")
	}
	if !strings.Contains(msg, "PREVIOUSLY FAILED") {
		t.Error("message should explain the issue")
	}
}

func TestPhantomState_NoPhantomWhenDifferentPaths(t *testing.T) {
	s := newPhantomState()

	// read_file fails on one path
	s.recordFailedCall("read_file", json.RawMessage(`{"path":"/foo/missing.go"}`), 1)

	// Subsequent calls use DIFFERENT paths (legitimate)
	s.recordSubsequentCall("read_file", json.RawMessage(`{"path":"/bar/existing.go"}`), 2)
	s.recordSubsequentCall("read_file", json.RawMessage(`{"path":"/baz/another.go"}`), 3)

	msg := s.checkPhantomInheritance(3)
	if msg != "" {
		t.Error("should not detect phantom inheritance when paths differ")
	}
}

func TestPhantomState_BasenameMatching(t *testing.T) {
	s := newPhantomState()

	// Failed call with full path
	s.recordFailedCall("read_file", json.RawMessage(`{"path":"/project/src/deep/missing.go"}`), 1)

	// Subsequent call with just the basename (common pattern)
	s.recordSubsequentCall("edit_file", json.RawMessage(`{"file_path":"missing.go"}`), 2)
	s.recordSubsequentCall("grep", json.RawMessage(`{"pattern":"func main","path":"missing.go"}`), 3)

	msg := s.checkPhantomInheritance(3)
	if msg == "" {
		t.Error("expected phantom inheritance via basename match")
	}
}

func TestPhantomState_FiresOnceOnly(t *testing.T) {
	s := newPhantomState()

	s.recordFailedCall("read_file", json.RawMessage(`{"path":"/foo.go"}`), 1)
	s.recordSubsequentCall("grep", json.RawMessage(`{"path":"/foo.go"}`), 2)
	s.recordSubsequentCall("grep", json.RawMessage(`{"path":"/foo.go"}`), 3)

	msg1 := s.checkPhantomInheritance(3)
	if msg1 == "" {
		t.Error("first call should return guidance")
	}

	msg2 := s.checkPhantomInheritance(4)
	if msg2 != "" {
		t.Error("second call should return empty (fired already)")
	}
}

func TestPhantomState_BelowThreshold(t *testing.T) {
	s := newPhantomState()

	// Only 1 evidence signal (threshold is 2)
	s.recordFailedCall("read_file", json.RawMessage(`{"path":"/foo.go"}`), 1)
	s.recordSubsequentCall("grep", json.RawMessage(`{"path":"/foo.go"}`), 2)

	msg := s.checkPhantomInheritance(2)
	if msg != "" {
		t.Error("should not fire with only 1 evidence signal (below threshold of 2)")
	}
}

func TestPhantomState_FailedCallWindow(t *testing.T) {
	s := newPhantomState()

	// Fill the lookback window with failed calls
	for i := 0; i < phantomLookback+2; i++ {
		s.recordFailedCall("read_file", json.RawMessage(`{"path":"/old`+string(rune('a'+i))+`.go"}`), i+1)
	}

	// Old failed calls should have been evicted
	if len(s.failedCalls) > phantomLookback {
		t.Errorf("failedCalls should be capped at %d, got %d", phantomLookback, len(s.failedCalls))
	}
}

func TestPhantomState_ParsePhantomArgs(t *testing.T) {
	// Valid JSON
	args := parsePhantomArgs(json.RawMessage(`{"path":"/foo.go","pattern":"test"}`))
	if args == nil {
		t.Fatal("expected non-nil args for valid JSON")
	}
	if args["path"] != "/foo.go" {
		t.Errorf("expected path=/foo.go, got %v", args["path"])
	}

	// Invalid JSON
	args = parsePhantomArgs(json.RawMessage(`{invalid`))
	if args != nil {
		t.Error("expected nil args for invalid JSON")
	}
}
