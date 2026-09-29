package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newSupersedeTestAM(t *testing.T) *AutoMemory {
	t.Helper()
	am := NewAutoMemory()
	am.dir = t.TempDir()
	return am
}

func mustSaveMemory(t *testing.T, am *AutoMemory, key, content string) {
	t.Helper()
	if err := am.SaveMemory(key, content); err != nil {
		t.Fatalf("save %s: %v", key, err)
	}
}

// DetectSupersession requires explicit replacement semantics: a plain
// conflicting save must NOT retire anything (warn-only path unchanged).
func TestDetectSupersession_PlainConflictNoMarker(t *testing.T) {
	am := newSupersedeTestAM(t)
	mustSaveMemory(t, am, "build-process", "build command: go build ./...")

	olds := am.DetectSupersession("build-new", "build command: go build -tags goolm ./...")
	if len(olds) != 0 {
		t.Fatalf("plain conflict must not supersede, got %v", olds)
	}
}

// Explicit marker + claim conflict retires the old entry.
func TestDetectSupersession_MarkerAndConflict(t *testing.T) {
	am := newSupersedeTestAM(t)
	mustSaveMemory(t, am, "build-process", "build command: go build ./...")

	olds := am.DetectSupersession("build-v2", "build command: go build -tags goolm ./... (replaces the old command)")
	if len(olds) != 1 || olds[0] != "build-process" {
		t.Fatalf("expected [build-process], got %v", olds)
	}
}

// Marker without a claim conflict does not retire unrelated entries.
func TestDetectSupersession_MarkerNoConflict(t *testing.T) {
	am := newSupersedeTestAM(t)
	mustSaveMemory(t, am, "build-process", "build command: go build ./...")

	olds := am.DetectSupersession("deploy-v2", "deploy target: staging (switched to per-tenant deploys)")
	if len(olds) != 0 {
		t.Fatalf("no subject overlap must not supersede, got %v", olds)
	}
}

// Passive voice ("superseded by") is not a marker: the new entry describes
// being replaced, it must not retire the entry it conflicts with.
func TestDetectSupersession_PassiveVoiceNotMarker(t *testing.T) {
	am := newSupersedeTestAM(t)
	mustSaveMemory(t, am, "build-process", "build command: go build ./...")

	olds := am.DetectSupersession("build-legacy", "build command: make all (superseded by go build)")
	if len(olds) != 0 {
		t.Fatalf("passive voice must not supersede, got %v", olds)
	}
}

// CJK replacement markers count.
func TestDetectSupersession_CJKMarker(t *testing.T) {
	am := newSupersedeTestAM(t)
	mustSaveMemory(t, am, "build-process", "build command: go build ./...")

	olds := am.DetectSupersession("build-v2", "build command: go build -tags goolm ./...（改用 goolm tag）")
	if len(olds) != 1 || olds[0] != "build-process" {
		t.Fatalf("CJK marker should supersede, got %v", olds)
	}
}

// Self-update (same key) never retires itself.
func TestDetectSupersession_SelfKeySkipped(t *testing.T) {
	am := newSupersedeTestAM(t)
	mustSaveMemory(t, am, "build-process", "build command: go build ./...")

	olds := am.DetectSupersession("build-process", "build command: go build -tags goolm ./... (replaces previous)")
	if len(olds) != 0 {
		t.Fatalf("self must be skipped, got %v", olds)
	}
}

// Detection caps at maxSupersedeTargets even with many conflicting entries.
func TestDetectSupersession_Cap(t *testing.T) {
	am := newSupersedeTestAM(t)
	for i := 0; i < 5; i++ {
		key := "build-" + string(rune('a'+i))
		mustSaveMemory(t, am, key, "build command: go build ./...")
	}
	olds := am.DetectSupersession("build-new", "build command: go build -tags goolm ./... (replaces all)")
	if len(olds) != maxSupersedeTargets {
		t.Fatalf("expected cap %d, got %d", maxSupersedeTargets, len(olds))
	}
}

// Apply + loadForPrompt: superseded entries drop out of prompt injection but
// stay on disk (read_file still works).
func TestApplySupersession_RetiresFromPromptKeepsFile(t *testing.T) {
	am := newSupersedeTestAM(t)
	mustSaveMemory(t, am, "build-process", "build command: go build ./...")
	mustSaveMemory(t, am, "build-v2", "build command: go build -tags goolm ./... (replaces old)")

	olds := am.DetectSupersession("build-v2", "build command: go build -tags goolm ./... (replaces old)")
	if len(olds) != 1 {
		t.Fatalf("detect failed: %v", olds)
	}
	if err := am.ApplySupersession("build-v2", olds); err != nil {
		t.Fatal(err)
	}

	set := am.SupersededSet()
	if !set["build-process"] {
		t.Fatalf("build-process should be superseded, set=%v", set)
	}
	if set["build-v2"] {
		t.Fatalf("winner must stay active")
	}

	// File remains on disk for history.
	if _, err := os.Stat(filepath.Join(am.dir, "build-process.md")); err != nil {
		t.Fatalf("history file must survive: %v", err)
	}

	inline, indexOnly, err := am.LoadForPrompt()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range inline {
		if e.Key == "build-process" {
			t.Fatalf("superseded entry must not be inlined")
		}
	}
	for _, k := range indexOnly {
		if k == "build-process" {
			t.Fatalf("superseded entry must not be indexed")
		}
	}
	found := false
	for _, e := range inline {
		if e.Key == "build-v2" {
			found = true
		}
	}
	for _, k := range indexOnly {
		if k == "build-v2" {
			found = true
		}
	}
	if !found {
		t.Fatalf("winner should remain present (indexOnly=%v inline=%v)", indexOnly, inline)
	}
}

// Revival: re-saving a retired key with replacement intent restores it.
func TestApplySupersession_Revival(t *testing.T) {
	am := newSupersedeTestAM(t)
	mustSaveMemory(t, am, "old-way", "test runner: jest")
	mustSaveMemory(t, am, "new-way", "test runner: vitest (replaces jest)")
	if err := am.ApplySupersession("new-way", []string{"old-way"}); err != nil {
		t.Fatal(err)
	}

	// Later, the agent re-asserts old-way explicitly.
	if err := am.ApplySupersession("old-way", nil); err != nil {
		t.Fatal(err)
	}
	set := am.SupersededSet()
	if set["old-way"] {
		t.Fatalf("old-way should be revived")
	}
	if !set["new-way"] && set["old-way"] {
		t.Fatalf("state inconsistent")
	}
}

// Chain update: superseding the same old key again points to the latest
// winner instead of accumulating stale edges.
func TestApplySupersession_ChainUpdate(t *testing.T) {
	am := newSupersedeTestAM(t)
	mustSaveMemory(t, am, "legacy", "build command: make")

	if err := am.ApplySupersession("v2", []string{"legacy"}); err != nil {
		t.Fatal(err)
	}
	if err := am.ApplySupersession("v3", []string{"legacy"}); err != nil {
		t.Fatal(err)
	}

	idx := am.loadSuperseded()
	rec := idx.Entries["legacy"]
	if rec == nil || rec.By != "v3" {
		t.Fatalf("latest winner should own the edge, got %+v", rec)
	}
}

// Sidecar persists across AutoMemory instances over the same directory.
func TestSupersedeSidecar_Persistence(t *testing.T) {
	dir := t.TempDir()
	am1 := NewAutoMemory()
	am1.dir = dir
	if err := am1.ApplySupersession("winner", []string{"loser"}); err != nil {
		t.Fatal(err)
	}

	am2 := NewAutoMemory()
	am2.dir = dir
	set := am2.SupersededSet()
	if !set["loser"] || set["winner"] {
		t.Fatalf("sidecar must persist, set=%v", set)
	}
}

// A corrupt sidecar degrades to "no supersessions" instead of failing.
func TestSupersedeSidecar_CorruptFileDegrades(t *testing.T) {
	am := newSupersedeTestAM(t)
	if err := os.WriteFile(filepath.Join(am.dir, supersedeSidecarFile), []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}
	set := am.SupersededSet()
	if len(set) != 0 {
		t.Fatalf("corrupt sidecar must degrade to empty, got %v", set)
	}
}

func TestFormatSupersedeNote(t *testing.T) {
	if got := FormatSupersedeNote(nil); got != "" {
		t.Fatalf("empty note expected, got %q", got)
	}
	got := FormatSupersedeNote([]string{"a", "b"})
	if !strings.Contains(got, "a, b") || !strings.Contains(got, "2") {
		t.Fatalf("unexpected note: %q", got)
	}
}
