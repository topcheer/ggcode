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

	g := v.record(mkToolCall("read_file", map[string]interface{}{
		"path": "/foo/bar.go",
	}), 1)
	if g != "" {
		t.Fatalf("first read should not trigger: got %q", g)
	}

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

	g := v.record(mkToolCall("read_file", map[string]interface{}{
		"path": "/foo/a.go",
	}), 1)
	if g != "" {
		t.Fatalf("first read should not trigger: got %q", g)
	}

	g = v.record(mkToolCall("read_file", map[string]interface{}{
		"path": "/foo/b.go",
	}), 2)
	if g != "" {
		t.Fatalf("second read should not trigger: got %q", g)
	}

	g = v.record(mkToolCall("read_file", map[string]interface{}{
		"path": "/foo/c.go",
	}), 3)
	if g == "" {
		t.Fatal("third sequential read should trigger batch suggestion")
	}
}

func TestToolSequence_SequentialReadsSameFile(t *testing.T) {
	v := newToolSequenceValidator()

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

	g := v.record(mkToolCall("grep", map[string]interface{}{
		"pattern": "TODO",
	}), 1)
	if g != "" {
		t.Fatalf("broad search should not trigger: got %q", g)
	}

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

	g := v.record(mkToolCall("read_file", map[string]interface{}{"path": "/a.go"}), 1)
	if g != "" {
		t.Fatalf("first read should not trigger: got %q", g)
	}

	g = v.record(mkToolCall("read_file", map[string]interface{}{"path": "/b.go"}), 2)
	if g != "" {
		t.Fatalf("second read should not trigger: got %q", g)
	}

	g = v.record(mkToolCall("edit_file", map[string]interface{}{
		"file_path": "/a.go",
	}), 3)
	if g != "" {
		t.Fatalf("edit_file should not trigger: got %q", g)
	}

	g = v.record(mkToolCall("read_file", map[string]interface{}{"path": "/c.go"}), 4)
	if g != "" {
		t.Fatalf("read after edit should not trigger sequential hint: got %q", g)
	}
}

func TestBroadThenNarrowSearch_SearchFiles(t *testing.T) {

	v := &toolSequenceValidator{history: []seqEntry{}, hintsGiven: map[string]bool{}}

	v.record(mkToolCall("search_files", map[string]interface{}{
		"pattern": "TODO",
	}), 1)

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
