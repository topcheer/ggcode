package agent

import (
	"encoding/json"
	"testing"
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
