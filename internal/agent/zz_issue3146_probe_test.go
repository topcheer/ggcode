package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write3146Store(t *testing.T, dir string, entries []PlaybookFailureEntry) {
	t.Helper()
	b, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".ggcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ggcode", "playbook_failures.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestIssue3146_PruneEvictsExpired(t *testing.T) {
	now := time.Now()
	in := []PlaybookFailureEntry{
		{ID: "fresh", TaskType: "fix", LastSeen: now.Add(-time.Hour), Occurrences: 1},
		{ID: "expired", TaskType: "fix", LastSeen: now.Add(-failureEntryTTL - 24*time.Hour), Occurrences: 99},
	}
	got := pruneFailureEntries(in)
	if len(got) != 1 || got[0].ID != "fresh" {
		t.Fatalf("expired entry survived prune: %+v", got)
	}
}

func TestIssue3146_RenderSkipsStale(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	write3146Store(t, dir, []PlaybookFailureEntry{
		{ID: "old", TaskType: "fix", SuspectTool: "edit_file", SuspectFile: "a/old.go",
			MaxCRS: 80, Occurrences: 9, FirstSeen: now.Add(-30 * 24 * time.Hour),
			LastSeen: now.Add(-20 * 24 * time.Hour)},
		{ID: "fresh", TaskType: "fix", SuspectTool: "edit_file", SuspectFile: "a/fresh.go",
			MaxCRS: 50, Occurrences: 2, FirstSeen: now.Add(-2 * 24 * time.Hour),
			LastSeen: now.Add(-time.Hour)},
	})
	hints := FailureHintsForPrompt(dir, "please fix the bug in parser", 2)
	if strings.Contains(hints, "old.go") {
		t.Fatalf("stale entry injected (20d unseen): %s", hints)
	}
	if !strings.Contains(hints, "fresh.go") {
		t.Fatalf("fresh entry missing from hints: %s", hints)
	}
}

func TestIssue3146_AllStaleRendersEmpty(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	write3146Store(t, dir, []PlaybookFailureEntry{
		{ID: "old", TaskType: "fix", SuspectTool: "edit_file", SuspectFile: "a/old.go",
			MaxCRS: 80, Occurrences: 9, FirstSeen: now.Add(-40 * 24 * time.Hour),
			LastSeen: now.Add(-30 * 24 * time.Hour)},
	})
	if got := FailureHintsForPrompt(dir, "fix anything", 2); got != "" {
		t.Fatalf("all-stale store rendered non-empty: %q", got)
	}
}

func TestIssue3146_FreshStillRenders(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	write3146Store(t, dir, []PlaybookFailureEntry{
		{ID: "e", TaskType: "fix", SuspectTool: "edit_file", SuspectFile: "a/p.go",
			MaxCRS: 60, Occurrences: 3, FirstSeen: now.Add(-3 * 24 * time.Hour),
			LastSeen: now.Add(-time.Hour)},
	})
	hints := FailureHintsForPrompt(dir, "please fix the bug in p", 2)
	if !strings.Contains(hints, "recurring suspect") {
		t.Fatalf("fresh recurring entry lost its note: %s", hints)
	}
}

func TestIssue3146_TruncationAtLineBoundary(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	entries := make([]PlaybookFailureEntry, 8)
	for i := range entries {
		entries[i] = PlaybookFailureEntry{
			ID: "e", TaskType: "fix", SuspectTool: "edit_file",
			SuspectFile: "a/中文文件名很长的文件.go",
			MaxCRS:      60, Occurrences: 3,
			FirstSeen: now.Add(-time.Hour), LastSeen: now.Add(-time.Hour),
		}
	}
	write3146Store(t, dir, entries)
	hints := FailureHintsForPrompt(dir, "please fix the bug", 8)
	if strings.ToValidUTF8(hints, "") != hints {
		t.Fatalf("truncated hints contain invalid UTF-8: %q", hints)
	}
	if len(hints) > maxFailureHintChars {
		t.Fatalf("hints exceed cap: %d > %d", len(hints), maxFailureHintChars)
	}
}
