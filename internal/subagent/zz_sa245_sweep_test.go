package subagent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func withTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func TestSweepStaleWorkspaceDirs(t *testing.T) {
	withTestHome(t)
	root := SubagentsRoot()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}

	current := NewTemplateStore(t.TempDir()).dir
	stale1 := filepath.Join(root, "aaaa1111aaaa1111")
	stale2 := filepath.Join(root, "bbbb2222bbbb2222")
	fresh := filepath.Join(root, "cccc3333cccc3333")
	for _, d := range []string{current, stale1, stale2, fresh} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "tpl.json"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	old := time.Now().Add(-91 * 24 * time.Hour)
	for _, d := range []string{stale1, stale2} {
		if err := os.Chtimes(d, old, old); err != nil {
			t.Fatal(err)
		}
	}

	ws := t.TempDir()
	keep := NewTemplateStore(ws).dir
	if err := os.MkdirAll(keep, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(keep, old, old); err != nil {
		t.Fatal(err)
	}

	removed := SweepStaleWorkspaceDirs(ws, 90*24*time.Hour)

	if removed != 2 {
		t.Fatalf("removed = %d, want 2 (stale dirs only)", removed)
	}
	for _, d := range []string{stale1, stale2} {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Errorf("stale dir %s must be removed, stat err=%v", filepath.Base(d), err)
		}
	}
	for _, d := range []string{fresh, keep} {
		if _, err := os.Stat(d); err != nil {
			t.Errorf("dir %s must survive, err=%v", filepath.Base(d), err)
		}
	}
}

func TestSweepStaleWorkspaceDirsNoRoot(t *testing.T) {
	withTestHome(t)
	if n := SweepStaleWorkspaceDirs(t.TempDir(), 90*24*time.Hour); n != 0 {
		t.Fatalf("sweep on missing root removed = %d, want 0", n)
	}
}
