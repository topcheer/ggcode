package extpane

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIssue1722WriteCappedTruncates(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "log")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ep := &ExtPane{LogFile: f, LogPath: p}
	big := strings.Repeat("x", maxExtPaneLogSize-8)
	writeExtCapped(ep, big)
	if fi, _ := os.Stat(p); fi.Size() == 0 {
		t.Fatal("first under-cap write must persist")
	}

	writeExtCapped(ep, "after-cap")
	b, _ := os.ReadFile(p)
	if string(b) != "after-cap" {
		t.Fatalf("post-cap file must contain only the new write, got %d bytes", len(b))
	}
}

func TestIssue1722ThreeSitesCapped(t *testing.T) {
	src, err := os.ReadFile("manager.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)

	n := strings.Count(s, "writeExtCapped(ep")
	if n < 4 {
		t.Fatalf("expected 3 call sites + definition, found %d - a write path lost its cap", n-1)
	}
	if strings.Count(s, "ep.LogFile.WriteString(") != 1 {
		t.Fatal("raw WriteString must exist only inside writeExtCapped")
	}
}
