package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// r460: learning-store topology probes (merge/export/backflow/global tier).

func mkR460(ts time.Time, cat, insight string) trajectoryLearning {
	return trajectoryLearning{Timestamp: ts, Type: "strategy", Category: cat, Insight: insight, Success: true}
}

func writeR460Store(t *testing.T, path string, entries []trajectoryLearning) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeTrajFile(path, entries); err != nil {
		t.Fatal(err)
	}
}

func TestTrajMergeInto_DedupesAndIsIdempotent(t *testing.T) {
	main := t.TempDir()
	src := filepath.Join(t.TempDir(), "src.jsonl")
	now := time.Now()
	writeR460Store(t, src, []trajectoryLearning{
		mkR460(now, "a", "alpha"),
		mkR460(now, "b", "beta"),
	})
	added, err := TrajMergeInto(main, src)
	if err != nil || added != 2 {
		t.Fatalf("first merge: added=%d err=%v", added, err)
	}
	added, err = TrajMergeInto(main, src)
	if err != nil || added != 0 {
		t.Fatalf("re-merge must be idempotent: added=%d err=%v", added, err)
	}
	// Partial dedupe: one new + one known.
	writeR460Store(t, src, []trajectoryLearning{
		mkR460(now, "a", "alpha"),
		mkR460(now, "c", "gamma"),
	})
	added, err = TrajMergeInto(main, src)
	if err != nil || added != 1 {
		t.Fatalf("partial merge: added=%d err=%v", added, err)
	}
	if got := TrajListLearnings(main); len(got) != 3 {
		t.Fatalf("store must hold 3 distinct, got %d", len(got))
	}
}

func TestTrajMergeInto_AbsentSourceNoop(t *testing.T) {
	main := t.TempDir()
	added, err := TrajMergeInto(main, filepath.Join(main, "nope.jsonl"))
	if err == nil || added != 0 {
		t.Fatalf("absent source must error without mutation: %d %v", added, err)
	}
}

func TestTrajExportImportRoundtrip(t *testing.T) {
	ws := t.TempDir()
	now := time.Now()
	writeR460Store(t, filepath.Join(ws, ".ggcode", "trajectory-learnings.jsonl"), []trajectoryLearning{
		mkR460(now, "a", "alpha"),
		mkR460(now, "b", "beta"),
	})
	out := filepath.Join(t.TempDir(), "exported.jsonl")
	n, err := TrajExportLearnings(ws, out)
	if err != nil || n != 2 {
		t.Fatalf("export: %d %v", n, err)
	}
	// Import into a fresh workspace.
	ws2 := t.TempDir()
	added, err := TrajMergeInto(ws2, out)
	if err != nil || added != 2 {
		t.Fatalf("import into fresh ws: %d %v", added, err)
	}
	if got := TrajListLearnings(ws2); len(got) != 2 {
		t.Fatalf("imported store must hold 2, got %d", len(got))
	}
}

func TestTrajBackflowFromWorktree(t *testing.T) {
	mainDir := t.TempDir()
	wt := t.TempDir() // stands in for the transient worktree
	now := time.Now()
	writeR460Store(t, filepath.Join(wt, ".ggcode", "trajectory-learnings.jsonl"), []trajectoryLearning{
		mkR460(now, "iso", "worktree-only insight"),
	})
	TrajBackflowFromWorktree(mainDir, wt)
	got := TrajListLearnings(mainDir)
	if len(got) != 1 || !strings.Contains(got[0].Insight, "worktree-only") {
		t.Fatalf("backflow must fold isolated learnings: %+v", got)
	}
	// Repeat backflow must not duplicate.
	TrajBackflowFromWorktree(mainDir, wt)
	if got := TrajListLearnings(mainDir); len(got) != 1 {
		t.Fatalf("repeat backflow must be idempotent, got %d", len(got))
	}
}

func TestTrajBackflow_NoStoreOrSameDirNoop(t *testing.T) {
	mainDir := t.TempDir()
	emptyWT := t.TempDir() // no learning store inside
	TrajBackflowFromWorktree(mainDir, emptyWT)
	if got := TrajListLearnings(mainDir); got != nil {
		t.Fatalf("no store in worktree must be a noop, got %d", len(got))
	}
	TrajBackflowFromWorktree(mainDir, mainDir) // same dir: self-merge guard
	if got := TrajListLearnings(mainDir); got != nil {
		t.Fatalf("same-dir backflow must be a noop, got %d", len(got))
	}
}

func TestRenderPromptSection_GlobalTierTopsUp(t *testing.T) {
	ws := t.TempDir()
	now := time.Now()
	// Workspace knows category "local"; global knows "local" + "general".
	writeR460Store(t, filepath.Join(ws, ".ggcode", "trajectory-learnings.jsonl"), []trajectoryLearning{
		mkR460(now, "local", "local insight"),
	})
	home := t.TempDir()
	t.Setenv("HOME", home) // redirect TrajGlobalPath for this test
	writeR460Store(t, filepath.Join(home, ".ggcode", "trajectory-learnings.jsonl"), []trajectoryLearning{
		mkR460(now, "local", "shadowed global"),
		mkR460(now, "general", "cross-project insight"),
	})
	s := newTrajIntelState()
	got := s.RenderPromptSection(ws)
	if !strings.Contains(got, "local insight") {
		t.Fatalf("workspace insight must inject: %q", got)
	}
	if !strings.Contains(got, "cross-project insight") {
		t.Fatalf("global-only category must top up: %q", got)
	}
	if strings.Contains(got, "shadowed global") {
		t.Fatalf("workspace must win its own category over global: %q", got)
	}
}
