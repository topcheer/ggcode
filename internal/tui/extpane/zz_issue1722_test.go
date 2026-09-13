package extpane

// #1722 case 2: every write path in this package used to be unbounded -
// cmdpane has capped+truncated at 5MB since forever. Pins the three
// capped write sites and the truncation behavior itself.

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
	// This write exceeds the cap: file truncated, then the new text lands.
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
	// The capped helper exists and is used at the buffer flush, the
	// immediate write, and the done flush - three formerly-uncapped sites.
	n := strings.Count(s, "writeExtCapped(ep")
	if n < 4 { // 3 call sites + 1 definition
		t.Fatalf("expected 3 call sites + definition, found %d - a write path lost its cap", n-1)
	}
	if strings.Count(s, "ep.LogFile.WriteString(") != 1 { // only inside the helper
		t.Fatal("raw WriteString must exist only inside writeExtCapped")
	}
}
