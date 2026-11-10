package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/tool"
	"testing"
	"time"
)

func TestUnreadEditState_BasicTracking(t *testing.T) {
	s := newUnreadEditState()

	if s.hasBeenRead("/foo/bar.go") {
		t.Fatal("expected file to be unread")
	}

	s.recordRead("/foo/bar.go")
	if !s.hasBeenRead("/foo/bar.go") {
		t.Fatal("expected file to be read after recordRead")
	}

	if hint := s.checkUnreadEdit("/foo/bar.go"); hint != "" {
		t.Fatalf("expected empty hint for read file, got: %s", hint)
	}
}

func TestUnreadEditState_WarnsOnUnread(t *testing.T) {
	s := newUnreadEditState()

	hint := s.checkUnreadEdit("/foo/baz.go")
	if hint == "" {
		t.Fatal("expected non-empty hint for unread file")
	}

	hint2 := s.checkUnreadEdit("/foo/baz.go")
	if hint2 != "" {
		t.Fatal("expected empty hint on second check (already warned)")
	}
}

func TestUnreadEditState_CreatedFilesExempt(t *testing.T) {
	s := newUnreadEditState()

	s.recordCreated("/foo/new.go")

	if hint := s.checkUnreadEdit("/foo/new.go"); hint != "" {
		t.Fatalf("expected no hint for created file, got: %s", hint)
	}
	if !s.hasBeenRead("/foo/new.go") {
		t.Fatal("created files should count as 'read'")
	}
}

func TestUnreadEditState_Reset(t *testing.T) {
	s := newUnreadEditState()

	s.recordRead("/foo/a.go")
	s.recordCreated("/foo/b.go")
	s.checkUnreadEdit("/foo/c.go")

	s.reset()

	if s.hasBeenRead("/foo/a.go") {
		t.Fatal("expected filesRead to be cleared after reset")
	}
	if s.hasBeenRead("/foo/b.go") {
		t.Fatal("expected filesCreated to be cleared after reset")
	}

	hint := s.checkUnreadEdit("/foo/c.go")
	if hint == "" {
		t.Fatal("expected warning after reset for previously-warned file")
	}
}

func TestUnreadEditState_PathNormalization(t *testing.T) {
	s := newUnreadEditState()

	s.recordRead("/foo/bar.go/")

	if !s.hasBeenRead("/foo/bar.go") {
		t.Fatal("expected path normalization to match")
	}
	if !s.hasBeenRead("  /foo/bar.go  ") {
		t.Fatal("expected whitespace trimming to match")
	}
}

func TestUnreadEditState_EmptyPath(t *testing.T) {
	s := newUnreadEditState()

	s.recordRead("")
	if hint := s.checkUnreadEdit(""); hint != "" {
		t.Fatalf("expected empty hint for empty path, got: %s", hint)
	}
}

func TestExtractEditFilePaths(t *testing.T) {
	t.Run("edit_file", func(t *testing.T) {
		args, _ := json.Marshal(map[string]string{"file_path": "/foo/bar.go"})
		paths := extractEditFilePaths("edit_file", args)
		if len(paths) != 1 || paths[0] != "/foo/bar.go" {
			t.Fatalf("expected [/foo/bar.go], got %v", paths)
		}
	})

	t.Run("multi_edit_file", func(t *testing.T) {
		args, _ := json.Marshal(map[string]string{"file_path": "/foo/baz.go"})
		paths := extractEditFilePaths("multi_edit_file", args)
		if len(paths) != 1 || paths[0] != "/foo/baz.go" {
			t.Fatalf("expected [/foo/baz.go], got %v", paths)
		}
	})

	t.Run("multi_file_edit", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{
			"files": []any{
				map[string]any{"path": "/a.go"},
				map[string]any{"path": "/b.go"},
			},
		})
		paths := extractEditFilePaths("multi_file_edit", args)
		if len(paths) != 2 {
			t.Fatalf("expected 2 paths, got %v", paths)
		}
	})

	t.Run("non_edit_tool", func(t *testing.T) {
		args, _ := json.Marshal(map[string]string{"path": "/foo/bar.go"})
		paths := extractEditFilePaths("read_file", args)
		if paths != nil {
			t.Fatalf("expected nil for read_file, got %v", paths)
		}
	})

	t.Run("file_ops_mkdir_skipped", func(t *testing.T) {

		args, _ := json.Marshal(map[string]any{
			"operations": []any{
				map[string]any{"action": "mkdir", "source": "/Volumes/new/brand-new-dir"},
			},
		})
		paths := extractEditFilePaths("file_ops", args)
		if paths != nil {
			t.Fatalf("expected nil for mkdir op, got %v", paths)
		}
	})

	t.Run("file_ops_all_ops_excluded", func(t *testing.T) {
		// file_ops delete/move/mkdir are not content edits: extracting their
		// paths made the unread-edit guard append "editing without reading"
		// hints to every batch delete (live misfire screenshot, 2026-11-07).
		args, _ := json.Marshal(map[string]any{
			"operations": []any{
				map[string]any{"action": "delete", "source": "/a/old.go"},
				map[string]any{"action": "move", "source": "/b/x.go", "destination": "/b/y.go"},
				map[string]any{"action": "mkdir", "source": "/c/dir"},
			},
		})
		paths := extractEditFilePaths("file_ops", args)
		if paths != nil {
			t.Fatalf("expected nil paths for all file_ops ops, got %v", paths)
		}
	})

	t.Run("batch_replace", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{
			"files":       []any{"/a/foo.go", "/b/bar.go"},
			"pattern":     "old",
			"replacement": "new",
		})
		paths := extractEditFilePaths("batch_replace", args)
		if len(paths) != 2 {
			t.Fatalf("expected 2 paths, got %d: %v", len(paths), paths)
		}
		if paths[0] != "/a/foo.go" || paths[1] != "/b/bar.go" {
			t.Fatalf("unexpected paths: %v", paths)
		}
	})
}

func TestExtractReadFilePaths(t *testing.T) {
	t.Run("read_file", func(t *testing.T) {
		args, _ := json.Marshal(map[string]string{"path": "/foo/bar.go"})
		paths := extractReadFilePaths("read_file", args)
		if len(paths) != 1 || paths[0] != "/foo/bar.go" {
			t.Fatalf("expected [/foo/bar.go], got %v", paths)
		}
	})

	t.Run("multi_file_read", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{
			"files": []any{
				map[string]any{"path": "/a.go"},
				map[string]any{"path": "/b.go"},
			},
		})
		paths := extractReadFilePaths("multi_file_read", args)
		if len(paths) != 2 {
			t.Fatalf("expected 2 paths, got %v", paths)
		}
	})
}

func TestExtractCreateFilePaths(t *testing.T) {
	t.Run("write_file", func(t *testing.T) {
		args, _ := json.Marshal(map[string]string{"path": "/foo/new.go"})
		paths := extractCreateFilePaths("write_file", args)
		if len(paths) != 1 || paths[0] != "/foo/new.go" {
			t.Fatalf("expected [/foo/new.go], got %v", paths)
		}
	})

	t.Run("multi_file_write", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{
			"files": []any{
				map[string]any{"path": "/a.go"},
				map[string]any{"path": "/b.go"},
			},
		})
		paths := extractCreateFilePaths("multi_file_write", args)
		if len(paths) != 2 {
			t.Fatalf("expected 2 paths, got %v", paths)
		}
	})

	t.Run("batch_replace", func(t *testing.T) {

		args, _ := json.Marshal(map[string]any{
			"files":       []any{"/x/y.go"},
			"pattern":     "a",
			"replacement": "b",
		})
		if paths := extractCreateFilePaths("batch_replace", args); len(paths) != 0 {
			t.Fatalf("batch_replace files must not be created, got %v", paths)
		}
	})
}

func TestStaleRead_NoWarningForUnreadFile(t *testing.T) {
	s := newUnreadEditState()

	if hint := s.checkStaleRead("/nonexistent/path.go"); hint != "" {
		t.Fatalf("expected empty hint for unread file, got: %s", hint)
	}
}

func TestStaleRead_WarnsOnExternalModification(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")

	if err := os.WriteFile(path, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	s := newUnreadEditState()
	s.recordRead(path)

	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}

	hint := s.checkStaleRead(path)
	if hint == "" {
		t.Fatal("expected stale-read warning after external modification")
	}
	if hint == "" || !strings.Contains(hint, "modified on disk") {
		t.Fatalf("expected warning about external modification, got: %s", hint)
	}
}

func TestStaleRead_NoWarningForUnchangedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")

	if err := os.WriteFile(path, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	s := newUnreadEditState()
	s.recordRead(path)

	if hint := s.checkStaleRead(path); hint != "" {
		t.Fatalf("expected no warning for unchanged file, got: %s", hint)
	}
}

func TestStaleRead_WarnsOnlyOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")

	if err := os.WriteFile(path, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	s := newUnreadEditState()
	s.recordRead(path)

	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}

	hint1 := s.checkStaleRead(path)
	if hint1 == "" {
		t.Fatal("expected first stale-read warning")
	}

	hint2 := s.checkStaleRead(path)
	if hint2 != "" {
		t.Fatal("expected no second stale-read warning (already warned)")
	}
}

func TestStaleRead_CreatedFilesExempt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.go")

	if err := os.WriteFile(path, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	s := newUnreadEditState()
	s.recordRead(path)
	s.recordCreated(path)

	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}

	if hint := s.checkStaleRead(path); hint != "" {
		t.Fatalf("expected no stale warning for agent-created file, got: %s", hint)
	}
}

func TestStaleRead_ResetClearsMtime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")

	if err := os.WriteFile(path, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	s := newUnreadEditState()
	s.recordRead(path)

	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}

	hint := s.checkStaleRead(path)
	if hint == "" {
		t.Fatal("expected stale-read warning before reset")
	}

	s.reset()

	hint2 := s.checkStaleRead(path)
	if hint2 != "" {
		t.Fatalf("expected no stale warning after reset, got: %s", hint2)
	}
}

func TestStaleRead_ReReadClearsStaleState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")

	if err := os.WriteFile(path, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	s := newUnreadEditState()
	s.recordRead(path)

	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}

	if hint := s.checkStaleRead(path); hint == "" {
		t.Fatal("expected first stale-read warning")
	}

	s.recordRead(path)

	if hint := s.checkStaleRead(path); hint != "" {
		t.Fatalf("expected no warning after re-read, got: %s", hint)
	}
}

func TestStaleRead_RewarnAfterReread(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")

	if err := os.WriteFile(path, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	s := newUnreadEditState()
	s.recordRead(path)

	t1 := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, t1, t1); err != nil {
		t.Fatal(err)
	}
	if hint := s.checkStaleRead(path); hint == "" {
		t.Fatal("expected first stale-read warning")
	}

	s.recordRead(path)

	t2 := t1.Add(2 * time.Second)
	if err := os.Chtimes(path, t2, t2); err != nil {
		t.Fatal(err)
	}
	if hint := s.checkStaleRead(path); hint == "" {
		t.Fatal("#162 regression: second external modification after re-read never re-warned")
	}
}

func TestStaleRead_OwnEditNotFlagged(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.go")
	if err := os.WriteFile(p, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newUnreadEditState()
	s.recordRead(p)
	s.recordWrite(p)
	if hint := s.checkStaleRead(p); hint != "" {
		t.Fatalf("own-edit refresh must not warn: %q", hint)
	}
}

func TestExtractCreateFilePathsExcludesBatchReplace(t *testing.T) {
	args := `{"pattern":"X","replacement":"Y","files":["/w/a.go","/w/b.go"]}`
	if got := extractCreateFilePaths("batch_replace", json.RawMessage(args)); len(got) != 0 {
		t.Fatalf("batch_replace files must not be treated as created, got %v", got)
	}

	if got := extractCreateFilePaths("write_file", json.RawMessage(`{"path":"/w/new.go","content":"x"}`)); len(got) != 1 {
		t.Fatalf("write_file must still extract created path, got %v", got)
	}
}

// #5374: the unread-edit hint must name the file - in multi-file edits N
// identical nameless lines are indistinguishable.
func TestUnreadEditHintNamesFile(t *testing.T) {
	s := newUnreadEditState()
	got := s.checkUnreadEdit("/tmp/whatever/alpha.go")
	if !strings.Contains(got, "alpha.go") {
		t.Fatalf("hint should name the file, got %q", got)
	}
}

// #5374: repeating the same hint for a second unread file is deduped at the
// injection layer (distinct texts still append).
func TestAppendGuidanceDedupesIdenticalText(t *testing.T) {
	a := &Agent{} // zero-value guidanceBudget is nil-map safe (allowDeduped lazy-inits)
	res := &tool.Result{}
	hint := "You are editing x.go without reading it first."
	if !a.appendGuidance(res, hint) {
		t.Fatal("first hint should append")
	}
	if a.appendGuidance(res, hint) {
		t.Fatal("identical hint should be deduped")
	}
	if strings.Count(res.Content, hint) != 1 {
		t.Fatalf("hint should appear exactly once, got %d", strings.Count(res.Content, hint))
	}
	// distinct hint appends (within budget: budget allows 2 hints/turn)
	other := "stale-read on y.go"
	if !a.appendGuidance(res, other) {
		t.Fatal("distinct hint should append")
	}
	// third distinct hint is budget-suppressed by design (guidanceBudgetPerTurn=2)
	third := "yet another z.go"
	if a.appendGuidance(res, third) {
		t.Fatal("third distinct hint should be budget-suppressed")
	}
}
