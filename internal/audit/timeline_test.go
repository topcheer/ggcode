package audit

import (
	"path/filepath"
	"testing"
)

// TestTimelineSessionFilter pins the entry-level export: session filter
// returns only matching entries; empty session returns everything.
func TestTimelineSessionFilter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	l, err := Open(path, "")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for _, ev := range []Event{
		{Tool: "grep", Status: "ok", InputHash: "a", Session: "s1"},
		{Tool: "edit_file", Status: "ok", InputHash: "b", Session: "s2", Rationale: "why"},
		{Tool: "run_command", Status: "ok", InputHash: "c", Session: "s1"},
	} {
		if _, err := l.Append(ev); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	all, err := Timeline(path, "")
	if err != nil {
		t.Fatalf("timeline all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("want 3 entries, got %d", len(all))
	}
	s1, err := Timeline(path, "s1")
	if err != nil {
		t.Fatalf("timeline s1: %v", err)
	}
	if len(s1) != 2 || s1[0].Session != "s1" || s1[1].Session != "s1" {
		t.Fatalf("s1 filter wrong: %+v", s1)
	}
	none, err := Timeline(path, "absent")
	if err != nil {
		t.Fatalf("timeline absent: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("absent session must yield zero entries, got %d", len(none))
	}
}
