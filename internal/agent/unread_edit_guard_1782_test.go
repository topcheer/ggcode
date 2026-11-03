package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func Test1782WindowedReadNotFullyRead(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.go")
	os.WriteFile(p, []byte("package main\n"), 0644)

	s := newUnreadEditState()
	s.recordReadWindow(p, true)
	if s.checkUnreadEdit(p) == "" {
		t.Fatal("windowed read must NOT satisfy the unread guard (edit outside the window would pass silently)")
	}
	s.recordReadWindow(p, false)
	if s.checkUnreadEdit(p) != "" {
		t.Fatal("full read must satisfy the unread guard")
	}
}
