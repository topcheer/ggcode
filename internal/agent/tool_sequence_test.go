package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func mkToolCall(name string, args map[string]interface{}) provider.ToolCallDelta {
	b, _ := json.Marshal(args)
	return provider.ToolCallDelta{
		Name:      name,
		Arguments: b,
	}
}

func TestToolSequence_FullReadThenTargeted(t *testing.T) {
	v := newToolSequenceValidator()

	// Full read (no offset/limit)
	g := v.record(mkToolCall("read_file", map[string]interface{}{
		"path": "/foo/bar.go",
	}), 1)
	if g != "" {
		t.Fatalf("first read should not trigger: got %q", g)
	}

	// Targeted re-read (with offset)
	g = v.record(mkToolCall("read_file", map[string]interface{}{
		"path":   "/foo/bar.go",
		"offset": 100,
		"limit":  50,
	}), 2)
	if g == "" {
		t.Fatal("full read then targeted should trigger guidance")
	}
	if v.hintsGiven["full_read_then_targeted"] != true {
		t.Fatal("hint should be marked as given")
	}

	// Subsequent same pattern should NOT fire again
	g = v.record(mkToolCall("read_file", map[string]interface{}{
		"path":   "/foo/bar.go",
		"offset": 200,
		"limit":  50,
	}), 3)
	if g != "" {
		t.Fatalf("should not fire again: got %q", g)
	}
}

func TestToolSequence_SequentialReads(t *testing.T) {
	v := newToolSequenceValidator()

	// First read - no hint
	g := v.record(mkToolCall("read_file", map[string]interface{}{
		"path": "/foo/a.go",
	}), 1)
	if g != "" {
		t.Fatalf("first read should not trigger: got %q", g)
	}

	// Second read - no hint
	g = v.record(mkToolCall("read_file", map[string]interface{}{
		"path": "/foo/b.go",
	}), 2)
	if g != "" {
		t.Fatalf("second read should not trigger: got %q", g)
	}

	// Third read - should trigger batch suggestion
	g = v.record(mkToolCall("read_file", map[string]interface{}{
		"path": "/foo/c.go",
	}), 3)
	if g == "" {
		t.Fatal("third sequential read should trigger batch suggestion")
	}
}

func TestToolSequence_SequentialReadsSameFile(t *testing.T) {
	v := newToolSequenceValidator()

	// Three reads of the SAME file should NOT trigger (handled by memoization)
	for i := 1; i <= 3; i++ {
		g := v.record(mkToolCall("read_file", map[string]interface{}{
			"path": "/foo/a.go",
		}), i)
		if g != "" {
			t.Fatalf("same-file reads should not trigger sequential hint: got %q at iter %d", g, i)
		}
	}
}

func TestToolSequence_DirThenGlob(t *testing.T) {
	v := newToolSequenceValidator()

	g := v.record(mkToolCall("list_directory", map[string]interface{}{
		"path": "/foo",
	}), 1)
	if g != "" {
		t.Fatalf("list_directory should not trigger: got %q", g)
	}

	g = v.record(mkToolCall("glob", map[string]interface{}{
		"pattern":   "*.go",
		"directory": "/foo",
	}), 2)
	if g == "" {
		t.Fatal("list_directory then glob on same dir should trigger")
	}
}

func TestToolSequence_ReadThenGrep(t *testing.T) {
	v := newToolSequenceValidator()

	g := v.record(mkToolCall("read_file", map[string]interface{}{
		"path": "/foo/bar.go",
	}), 1)
	if g != "" {
		t.Fatalf("read_file should not trigger: got %q", g)
	}

	g = v.record(mkToolCall("grep", map[string]interface{}{
		"pattern": "TODO",
		"path":    "/foo/bar.go",
	}), 2)
	if g == "" {
		t.Fatal("read_file then grep same file should trigger")
	}
}

func TestToolSequence_BroadThenNarrowSearch(t *testing.T) {
	v := newToolSequenceValidator()

	// Broad search (no directory)
	g := v.record(mkToolCall("grep", map[string]interface{}{
		"pattern": "TODO",
	}), 1)
	if g != "" {
		t.Fatalf("broad search should not trigger: got %q", g)
	}

	// Narrowed search (same pattern, now with path)
	g = v.record(mkToolCall("grep", map[string]interface{}{
		"pattern": "TODO",
		"path":    "/foo",
	}), 2)
	if g == "" {
		t.Fatal("broad then narrow search should trigger")
	}
}

func TestToolSequence_Reset(t *testing.T) {
	v := newToolSequenceValidator()

	// Trigger a hint
	_ = v.record(mkToolCall("read_file", map[string]interface{}{"path": "/a.go"}), 1)
	_ = v.record(mkToolCall("read_file", map[string]interface{}{"path": "/a.go", "offset": 10}), 2)
	if len(v.hintsGiven) == 0 {
		t.Fatal("expected hints to be given before reset")
	}

	v.reset()

	if len(v.hintsGiven) != 0 {
		t.Fatal("hintsGiven should be cleared after reset")
	}
	if len(v.history) != 0 {
		t.Fatal("history should be cleared after reset")
	}
}

func TestToolSequence_NoFalsePositiveEdit(t *testing.T) {
	v := newToolSequenceValidator()

	// edit_file calls should not trigger read patterns
	g := v.record(mkToolCall("edit_file", map[string]interface{}{
		"file_path": "/foo/bar.go",
	}), 1)
	if g != "" {
		t.Fatalf("edit_file should not trigger: got %q", g)
	}

	g = v.record(mkToolCall("write_file", map[string]interface{}{
		"path": "/foo/baz.go",
	}), 2)
	if g != "" {
		t.Fatalf("write_file should not trigger: got %q", g)
	}
}

func TestToolSequence_BoundaryNoHint(t *testing.T) {
	v := newToolSequenceValidator()

	// Two reads of different files should NOT trigger (need 3)
	g := v.record(mkToolCall("read_file", map[string]interface{}{"path": "/a.go"}), 1)
	if g != "" {
		t.Fatalf("first read should not trigger: got %q", g)
	}

	g = v.record(mkToolCall("read_file", map[string]interface{}{"path": "/b.go"}), 2)
	if g != "" {
		t.Fatalf("second read should not trigger: got %q", g)
	}

	// An intervening edit should break the consecutive read chain
	g = v.record(mkToolCall("edit_file", map[string]interface{}{
		"file_path": "/a.go",
	}), 3)
	if g != "" {
		t.Fatalf("edit_file should not trigger: got %q", g)
	}

	// This is the 2nd consecutive read (after the edit), not 3rd
	g = v.record(mkToolCall("read_file", map[string]interface{}{"path": "/c.go"}), 4)
	if g != "" {
		t.Fatalf("read after edit should not trigger sequential hint: got %q", g)
	}
}

func TestBroadThenNarrowSearch_SearchFiles(t *testing.T) {
	// Regression test for #99: search_files uses "directory" param, not "path".
	// The broad-then-narrow detector should fire for search_files.
	v := &toolSequenceValidator{history: []seqEntry{}, hintsGiven: map[string]bool{}}

	// Step 1: Broad search_files with no directory
	v.record(mkToolCall("search_files", map[string]interface{}{
		"pattern": "TODO",
	}), 1)

	// Step 2: Narrow search_files with directory
	g := v.record(mkToolCall("search_files", map[string]interface{}{
		"pattern":   "TODO",
		"directory": "/foo",
	}), 2)

	if g == "" {
		t.Fatal("expected broad-then-narrow hint for search_files, got empty")
	}
	if !strings.Contains(g, "TODO") || !strings.Contains(g, "/foo") {
		t.Fatalf("hint should mention pattern and dir, got: %s", g)
	}
}

func TestBroadThenNarrowSearch_GrepPath(t *testing.T) {
	// Ensure grep still works with "path" param after the fix.
	v := &toolSequenceValidator{history: []seqEntry{}, hintsGiven: map[string]bool{}}

	v.record(mkToolCall("grep", map[string]interface{}{
		"pattern": "FIXME",
	}), 1)

	g := v.record(mkToolCall("grep", map[string]interface{}{
		"pattern": "FIXME",
		"path":    "/bar",
	}), 2)

	if g == "" {
		t.Fatal("expected broad-then-narrow hint for grep, got empty")
	}
}
