package memory

// #3880 probes: deletion is visible to the as_of interval model
// (tombstone), and nested project-memory file order reverses layers while
// preserving the declared priority order within each layer.

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIssue3880_AsOfAfterDeleteFindsNothing(t *testing.T) {
	dir := t.TempDir()
	am := NewProjectAutoMemory(dir)
	if am == nil {
		t.Fatal("auto memory nil")
	}
	if err := am.SaveMemory("time-travel", "v1"); err != nil {
		t.Fatal(err)
	}
	midAt := time.Now() // after the save, before the delete
	time.Sleep(10 * time.Millisecond)
	if err := am.DeleteMemory("time-travel"); err != nil {
		t.Fatal(err)
	}
	delAt := time.Now()
	time.Sleep(10 * time.Millisecond)

	// After the deletion instant: not found (the old code returned v1).
	if _, _, found, _ := am.ReadMemoryAsOf("time-travel", delAt); found {
		t.Fatal("as_of after delete must find nothing (tombstone)")
	}
	// Before the deletion instant: the pre-delete content is still exact.
	content, _, found, err := am.ReadMemoryAsOf("time-travel", midAt)
	if err != nil || !found {
		t.Fatalf("as_of before delete must resolve the archived version: found=%v err=%v", found, err)
	}
	if content != "v1" {
		t.Fatalf("pre-delete content = %q, want v1", content)
	}
}

func TestIssue3880_NestedLayerOrderPreservedWithinLayer(t *testing.T) {
	root := t.TempDir()
	// Simulate a repo root (git dir with HEAD) and one nested dir, each
	// with both memory files.
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte("ref"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{root, filepath.Join(root, "pkg")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "GGCODE.md"), []byte("g"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "AGENTS.md"), []byte("a"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	files, err := NestedProjectMemoryFilesForPath(filepath.Join(root, "pkg", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	// Locate the two layers: every GGCODE.md must come before the AGENTS.md
	// of ITS OWN layer (the declared priority), and the deeper layer comes
	// first overall.
	var seq []string
	for _, f := range files {
		base := filepath.Base(f)
		if base == "GGCODE.md" || base == "AGENTS.md" {
			seq = append(seq, base+"@"+filepath.Base(filepath.Dir(f)))
		}
	}
	// Deeper layer comes LATER (higher priority overrides shallower);
	// within EACH layer the declared priority order (GGCODE before AGENTS)
	// must hold. The old flat reversal produced AGENTS-before-GGCODE inside
	// the layers.
	want := []string{
		"GGCODE.md@" + filepath.Base(root), "AGENTS.md@" + filepath.Base(root),
		"GGCODE.md@pkg", "AGENTS.md@pkg",
	}
	if len(seq) < 4 {
		t.Fatalf("expected two layers in output, got %v", seq)
	}
	for i := 0; i+3 < len(seq); i++ {
		if seq[i] == want[0] && seq[i+1] == want[1] && seq[i+2] == want[2] && seq[i+3] == want[3] {
			return // pass
		}
	}
	t.Fatalf("layer order wrong: within-layer priority must be preserved, got %v", seq)
}
