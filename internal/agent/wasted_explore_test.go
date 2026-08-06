package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWastedExploreReset(t *testing.T) {
	s := newWastedExploreState()
	s.nextID = 5
	s.currentIteration = 10
	s.injectionCount = 2
	s.pendingSearches[1] = &exploreInfo{ID: 1, ToolName: "grep"}
	s.consumedPaths["foo.go"] = true
	s.warned[1] = true

	s.reset()

	if len(s.pendingSearches) != 0 {
		t.Errorf("expected pendingSearches empty, got %d", len(s.pendingSearches))
	}
	if len(s.consumedPaths) != 0 {
		t.Errorf("expected consumedPaths empty, got %d", len(s.consumedPaths))
	}
	if len(s.warned) != 0 {
		t.Errorf("expected warned empty, got %d", len(s.warned))
	}
	if s.nextID != 0 || s.currentIteration != 0 || s.injectionCount != 0 {
		t.Errorf("reset failed: nextID=%d currentIteration=%d injectionCount=%d", s.nextID, s.currentIteration, s.injectionCount)
	}
}

func TestWastedExploreRecordSearch(t *testing.T) {
	s := newWastedExploreState()
	args := json.RawMessage(`{"pattern":"TODO"}`)
	result := "/path/to/file.go:42: TODO fix this\n/other/file.py:10: TODO that"

	s.recordSearchToolCall("grep", args, result, 1)

	if len(s.pendingSearches) != 1 {
		t.Fatalf("expected 1 pending search, got %d", len(s.pendingSearches))
	}
	for _, info := range s.pendingSearches {
		if info.ToolName != "grep" {
			t.Errorf("expected toolName grep, got %s", info.ToolName)
		}
		if len(info.FoundPaths) < 1 {
			t.Errorf("expected at least 1 found path, got %d", len(info.FoundPaths))
		}
		if info.SearchQuery != "TODO" {
			t.Errorf("expected query TODO, got %s", info.SearchQuery)
		}
	}
}

func TestWastedExploreRecordSearchEmptyResult(t *testing.T) {
	s := newWastedExploreState()
	args := json.RawMessage(`{"pattern":"NONEXISTENT"}`)

	s.recordSearchToolCall("grep", args, "No matches found", 1)

	if len(s.pendingSearches) != 0 {
		t.Errorf("expected 0 pending searches for empty result, got %d", len(s.pendingSearches))
	}
}

func TestWastedExploreConsumption(t *testing.T) {
	s := newWastedExploreState()

	// Record a search
	searchArgs := json.RawMessage(`{"pattern":"TODO"}`)
	searchResult := "/path/to/file.go:42: TODO fix this\n/other/file.py:10: TODO that"
	s.recordSearchToolCall("grep", searchArgs, searchResult, 1)

	// Record consumption of one of the found paths
	readArgs := json.RawMessage(`{"path":"/path/to/file.go"}`)
	s.recordConsumptionToolCall("read_file", readArgs, "file content", 2)

	for _, info := range s.pendingSearches {
		if !info.Consumed {
			t.Errorf("expected search to be consumed after reading one of its paths")
		}
	}
}

func TestWastedExploreNoConsumptionTriggersWarning(t *testing.T) {
	s := newWastedExploreState()

	// Record a search
	searchArgs := json.RawMessage(`{"pattern":"TODO"}`)
	searchResult := "/path/to/file.go:42: TODO fix this\n/other/file.py:10: TODO that"
	s.recordSearchToolCall("grep", searchArgs, searchResult, 1)

	// Check at iteration 1+2=3 (past window)
	msg := s.checkWastedSearches(3)

	if msg == "" {
		t.Fatal("expected wasted exploration warning, got empty")
	}
	if !strings.Contains(msg, "Wasted Exploration") {
		t.Errorf("expected 'Wasted Exploration' in message, got: %s", msg)
	}
	if !strings.Contains(msg, "grep") {
		t.Errorf("expected tool name 'grep' in message, got: %s", msg)
	}
}

func TestWastedExploreConsumedNoWarning(t *testing.T) {
	s := newWastedExploreState()

	// Record a search
	searchArgs := json.RawMessage(`{"pattern":"TODO"}`)
	searchResult := "/path/to/file.go:42: TODO fix this"
	s.recordSearchToolCall("grep", searchArgs, searchResult, 1)

	// Consume the path
	readArgs := json.RawMessage(`{"path":"/path/to/file.go"}`)
	s.recordConsumptionToolCall("read_file", readArgs, "content", 2)

	// Check at iteration 3 -- should NOT warn since consumed
	msg := s.checkWastedSearches(3)

	if msg != "" {
		t.Errorf("expected no warning for consumed search, got: %s", msg)
	}
}

func TestWastedExploreMaxInjections(t *testing.T) {
	s := newWastedExploreState()

	// Create multiple wasted searches
	for i := 0; i < 5; i++ {
		args := json.RawMessage(`{"pattern":"TODO"}`)
		result := "/path/to/file" + string(rune('a'+i)) + ".go:42: TODO fix"
		s.recordSearchToolCall("grep", args, result, 1)
	}

	// First check should warn
	msg1 := s.checkWastedSearches(3)
	if msg1 == "" {
		t.Fatal("expected first warning")
	}

	// Second check should also warn (injectionCount=1, still under cap)
	msg2 := s.checkWastedSearches(3)
	if msg2 == "" {
		t.Fatal("expected second warning")
	}

	// Third check should NOT warn (injectionCount=2, at cap)
	msg3 := s.checkWastedSearches(3)
	if msg3 != "" {
		t.Errorf("expected no more warnings after max injections, got: %s", msg3)
	}
}

func TestWastedExploreNonSearchToolIgnored(t *testing.T) {
	s := newWastedExploreState()
	args := json.RawMessage(`{"command":"ls"}`)
	s.recordSearchToolCall("run_command", args, "some output", 1)

	if len(s.pendingSearches) != 0 {
		t.Errorf("expected 0 pending searches for non-search tool, got %d", len(s.pendingSearches))
	}
}

func TestWastedExploreLooksLikeFilePath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"foo/bar.go", true},
		{"/abs/path/to/file.py", true},
		{"./relative/path.ts", true}, // starts with . and has recognized extension
		{"http://example.com", false},
		{"a sentence with spaces", false},
		{".go", false}, // too short, no path separator
		{"", false},
		{"foo/bar/baz.js", true},
		{"node_modules/react/index.js", true},
	}

	for _, tt := range tests {
		got := looksLikeFilePath(tt.path)
		if got != tt.want {
			t.Errorf("looksLikeFilePath(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestWastedExploreExtractPathsFromResult(t *testing.T) {
	result := `/path/to/file1.go:10: some content
/path/to/file2.py:20: more content
/another/deep/path/file3.ts:5: content here
No path line here`

	paths := extractFilePathsFromResult(result)
	if len(paths) < 2 {
		t.Fatalf("expected at least 2 paths, got %d: %v", len(paths), paths)
	}

	// Verify we found expected paths
	found := make(map[string]bool)
	for _, p := range paths {
		found[p] = true
	}
	if !found["/path/to/file1.go"] {
		t.Errorf("expected /path/to/file1.go in paths, got: %v", paths)
	}
}

func TestWastedExploreExtractPathsFromArgs(t *testing.T) {
	args := json.RawMessage(`{"path":"/some/file.go","pattern":"test"}`)
	paths := extractFilePathsFromArgs(args, "grep")
	if len(paths) == 0 {
		t.Fatal("expected at least 1 path")
	}
	found := false
	for _, p := range paths {
		if p == "/some/file.go" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected /some/file.go in extracted paths: %v", paths)
	}
}

func TestWastedExploreMultiFileReadArgs(t *testing.T) {
	args := json.RawMessage(`{"files":[{"path":"/a.go"},{"path":"/b.go"}]}`)
	paths := extractFilePathsFromArgs(args, "multi_file_read")
	if len(paths) < 2 {
		t.Fatalf("expected at least 2 paths, got %d: %v", len(paths), paths)
	}

	found := make(map[string]bool)
	for _, p := range paths {
		found[p] = true
	}
	if !found["/a.go"] || !found["/b.go"] {
		t.Errorf("expected /a.go and /b.go in paths: %v", paths)
	}
}

func TestWastedExploreAlreadyConsumedPaths(t *testing.T) {
	s := newWastedExploreState()

	// Pre-consume a path
	readArgs := json.RawMessage(`{"path":"/already/read.go"}`)
	s.recordConsumptionToolCall("read_file", readArgs, "content", 1)

	// Now do a search that finds the same path
	searchArgs := json.RawMessage(`{"pattern":"test"}`)
	searchResult := "/already/read.go:10: test content"
	s.recordSearchToolCall("grep", searchArgs, searchResult, 2)

	// The search should be immediately marked as consumed
	for _, info := range s.pendingSearches {
		if !info.Consumed {
			t.Errorf("expected search to be consumed because path was already accessed")
		}
	}
}

func TestWastedExploreQueryExtraction(t *testing.T) {
	tests := []struct {
		toolName string
		args     string
		want     string
	}{
		{"grep", `{"pattern":"TODO"}`, "TODO"},
		{"code_search", `{"query":"auth logic"}`, "auth logic"},
		{"glob", `{"pattern":"**/*.go"}`, "**/*.go"},
		{"search_files", `{"pattern":"func.*Main"}`, "func.*Main"},
	}

	for _, tt := range tests {
		args := json.RawMessage(tt.args)
		got := extractSearchQuery(args, tt.toolName)
		if got != tt.want {
			t.Errorf("extractSearchQuery(%s) = %q, want %q", tt.toolName, got, tt.want)
		}
	}
}

func TestWastedExploreCapPendingSearches(t *testing.T) {
	s := newWastedExploreState()

	// Fill beyond capacity
	for i := 0; i < wastedExploreMaxPending+5; i++ {
		args := json.RawMessage(`{"pattern":"test"}`)
		result := "/path/file" + string(rune('a'+i%26)) + ".go:1: test"
		s.recordSearchToolCall("grep", args, result, 1)
	}

	if len(s.pendingSearches) > wastedExploreMaxPending {
		t.Errorf("expected at most %d pending searches, got %d", wastedExploreMaxPending, len(s.pendingSearches))
	}
}
