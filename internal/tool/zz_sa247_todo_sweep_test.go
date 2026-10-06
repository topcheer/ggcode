package tool

// #3346 (sa-247 audit): todo files live at ~/.ggcode/todos/<sessionID>.json
// with no deletion path anywhere - same unbounded growth as sessions before
// #3337. SweepStaleTodoFiles must remove stale files, keep fresh ones and
// files of live sessions, and no-op when the dir does not exist.

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSweepStaleTodoFiles(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := todosDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(`[{"id":"1","content":"x","status":"done"}]`), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	stale1 := write("old-session-a.json")
	stale2 := write("old-session-b.json")
	fresh := write("recent-session.json")
	old := time.Now().Add(-31 * 24 * time.Hour)
	for _, p := range []string{stale1, stale2} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}

	if removed := SweepStaleTodoFiles(30 * 24 * time.Hour); removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
	for _, p := range []string{stale1, stale2} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("stale todo %s must be removed", filepath.Base(p))
		}
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("fresh todo must survive: %v", err)
	}
}

func TestSweepStaleTodoFilesNoDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no todos dir created
	if n := SweepStaleTodoFiles(30 * 24 * time.Hour); n != 0 {
		t.Fatalf("missing dir: removed = %d, want 0", n)
	}
}

func TestSweepStaleTodoFilesSkipsDirs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := todosDir()
	if err := os.MkdirAll(filepath.Join(dir, "subdir"), 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour * 24 * 400)
	if err := os.Chtimes(filepath.Join(dir, "subdir"), old, old); err != nil {
		t.Fatal(err)
	}
	if n := SweepStaleTodoFiles(30 * 24 * time.Hour); n != 0 {
		t.Fatalf("subdir must be skipped: removed = %d, want 0", n)
	}
	if _, err := os.Stat(filepath.Join(dir, "subdir")); err != nil {
		t.Errorf("subdir must not be removed: %v", err)
	}
}
