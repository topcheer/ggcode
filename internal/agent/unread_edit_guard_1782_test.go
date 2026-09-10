package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// #1782 case 3: a windowed read (offset/limit) must not mark the file fully
// read - an edit outside the window must still trip the unread guard.
func Test1782WindowedReadNotFullyRead(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.go")
	os.WriteFile(p, []byte("package main\n"), 0644)

	s := newUnreadEditState()
	s.recordReadWindow(p, true) // windowed
	if s.checkUnreadEdit(p) == "" {
		t.Fatal("windowed read must NOT satisfy the unread guard (edit outside the window would pass silently)")
	}
	s.recordReadWindow(p, false) // full read
	if s.checkUnreadEdit(p) != "" {
		t.Fatal("full read must satisfy the unread guard")
	}
}
