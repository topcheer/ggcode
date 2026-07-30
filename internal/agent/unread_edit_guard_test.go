package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUnreadEditState_BasicTracking(t *testing.T) {
	s := newUnreadEditState()

	// Initially, nothing is read.
	if s.hasBeenRead("/foo/bar.go") {
		t.Fatal("expected file to be unread")
	}

	// Record a read.
	s.recordRead("/foo/bar.go")
	if !s.hasBeenRead("/foo/bar.go") {
		t.Fatal("expected file to be read after recordRead")
	}

	// Should not warn for a file that was read.
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

	// Should warn only once per file.
	hint2 := s.checkUnreadEdit("/foo/baz.go")
	if hint2 != "" {
		t.Fatal("expected empty hint on second check (already warned)")
	}
}

func TestUnreadEditState_CreatedFilesExempt(t *testing.T) {
	s := newUnreadEditState()

	// write_file creates a file — no read needed.
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

	// After reset, everything should be unread again.
	if s.hasBeenRead("/foo/a.go") {
		t.Fatal("expected filesRead to be cleared after reset")
	}
	if s.hasBeenRead("/foo/b.go") {
		t.Fatal("expected filesCreated to be cleared after reset")
	}
	// Should be able to warn again after reset.
	hint := s.checkUnreadEdit("/foo/c.go")
	if hint == "" {
		t.Fatal("expected warning after reset for previously-warned file")
	}
}

func TestUnreadEditState_PathNormalization(t *testing.T) {
	s := newUnreadEditState()

	// Trailing slashes and spaces should be normalized.
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
}

// --- Stale-read detection tests ---

func TestStaleRead_NoWarningForUnreadFile(t *testing.T) {
	s := newUnreadEditState()
	// A file that was never read should not trigger stale-read.
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

	// Simulate external modification with a newer mtime.
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

	// No modification — should not warn.
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

	// First check should warn.
	hint1 := s.checkStaleRead(path)
	if hint1 == "" {
		t.Fatal("expected first stale-read warning")
	}
	// Second check should not warn (already warned).
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
	s.recordCreated(path) // Agent wrote this file after reading

	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}

	// Even though file changed on disk, agent authored it — no warning.
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

	// After reset, no mtime is tracked — should not warn.
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

	// First check warns.
	if hint := s.checkStaleRead(path); hint == "" {
		t.Fatal("expected first stale-read warning")
	}

	// Agent re-reads the file — this updates the mtime baseline.
	s.recordRead(path)

	// No further external modification — should not warn.
	if hint := s.checkStaleRead(path); hint != "" {
		t.Fatalf("expected no warning after re-read, got: %s", hint)
	}
}
