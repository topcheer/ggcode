package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func draftTestStats() *RunStats {
	s := newRunStats("fix the failing build in internal/provider")
	s.Success = true
	s.Iterations = 7
	s.Duration = 95 * time.Second
	s.ToolCalls["run_command"] = 5
	s.ToolCalls["read_file"] = 3
	s.ToolCalls["edit_file"] = 2
	s.recordCommand("# build check\ngo build -tags goolm ./...")
	s.recordCommand("# run tests\ngo test -tags goolm ./internal/provider")
	s.recordFileEdit("internal/provider/anthropic.go")
	s.finalize(nil)
	return s
}

func draftsDir(t *testing.T, workingDir string) string {
	t.Helper()
	return filepath.Join(workingDir, ".ggcode", skillDraftDirName)
}

func TestDistillSkillWritesDraft(t *testing.T) {
	dir := t.TempDir()
	if err := distillSkillToDir(dir, draftTestStats(), time.Now()); err != nil {
		t.Fatalf("distill failed: %v", err)
	}
	entries, err := os.ReadDir(draftsDir(t, dir))
	if err != nil {
		t.Fatalf("drafts dir missing: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 draft, got %d", len(entries))
	}
	if !strings.HasSuffix(entries[0].Name(), ".md") {
		t.Fatalf("draft is not markdown: %s", entries[0].Name())
	}
	data, err := os.ReadFile(filepath.Join(draftsDir(t, dir), entries[0].Name()))
	if err != nil {
		t.Fatalf("read draft: %v", err)
	}
	content := string(data)
	for _, want := range []string{
		"procedure-hash:",
		"## Observed procedure",
		"go test -tags goolm ./internal/provider",
		"## Files touched",
		"## Validation commands",
		"status: draft",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("draft missing %q", want)
		}
	}
}

func TestDistillSkillDedupeByHash(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	if err := distillSkillToDir(dir, draftTestStats(), now); err != nil {
		t.Fatalf("first distill failed: %v", err)
	}
	// Same procedure on a later run (different runID) must not duplicate.
	second := draftTestStats()
	if err := distillSkillToDir(dir, second, now.Add(time.Hour)); err == nil {
		t.Fatal("expected duplicate error, got nil")
	}
	entries, _ := os.ReadDir(draftsDir(t, dir))
	if len(entries) != 1 {
		t.Fatalf("expected 1 draft after dedupe, got %d", len(entries))
	}
}

func TestDistillSkillSkipUnqualified(t *testing.T) {
	dir := t.TempDir()

	// Failed run.
	failed := draftTestStats()
	failed.finalize(os.ErrPermission)
	if err := distillSkillToDir(dir, failed, time.Now()); err == nil {
		t.Error("expected skip for failed run")
	}

	// Too few tool calls.
	trivial := draftTestStats()
	trivial.ToolCalls = map[string]int{"read_file": 1}
	if err := distillSkillToDir(dir, trivial, time.Now()); err == nil {
		t.Error("expected skip for trivial run")
	}

	// No procedural content: no commands, no build+edit combo.
	chatty := draftTestStats()
	chatty.CommandsRun = nil
	chatty.FilesEdited = nil
	if err := distillSkillToDir(dir, chatty, time.Now()); err == nil {
		t.Error("expected skip for run without procedure")
	}

	if entries, _ := os.ReadDir(draftsDir(t, dir)); len(entries) != 0 {
		t.Errorf("expected no drafts, got %d", len(entries))
	}
}

func TestDistillSkillBuildEditComboQualifies(t *testing.T) {
	dir := t.TempDir()
	s := draftTestStats()
	s.CommandsRun = nil
	s.recordCommand("# verify\ngo build -tags goolm ./cmd/ggcode")
	if err := distillSkillToDir(dir, s, time.Now()); err != nil {
		t.Fatalf("build+edit combo should qualify: %v", err)
	}
}

func TestDistillSkillEvictionCap(t *testing.T) {
	dir := t.TempDir()
	draftDir := draftsDir(t, dir)
	if err := os.MkdirAll(draftDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Pre-seed maxSkillDrafts old drafts.
	base := time.Now().Add(-24 * time.Hour)
	for i := 0; i < maxSkillDrafts; i++ {
		name := filepath.Join(draftDir, "old-"+string(rune('a'+i))+".md")
		content := "---\nprocedure-hash: oldhash" + string(rune('a'+i)) + "\n---\n"
		if err := os.WriteFile(name, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		os.Chtimes(name, base, base)
	}
	// This new procedure must evict the oldest drafts.
	if err := distillSkillToDir(dir, draftTestStats(), time.Now()); err != nil {
		t.Fatalf("distill failed: %v", err)
	}
	entries, _ := os.ReadDir(draftDir)
	if len(entries) != maxSkillDrafts {
		t.Fatalf("expected cap %d drafts, got %d", maxSkillDrafts, len(entries))
	}
	// The newest draft must survive; the oldest ("old-a.md") must be gone.
	found := false
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "old-") {
			found = true
		}
		if e.Name() == "old-a.md" {
			t.Error("oldest draft was not evicted")
		}
	}
	if !found {
		t.Error("new draft missing after eviction")
	}
}

func TestDistillSkillPromptTruncation(t *testing.T) {
	dir := t.TempDir()
	s := draftTestStats()
	s.UserPrompt = strings.Repeat("x", 300)
	if err := distillSkillToDir(dir, s, time.Now()); err != nil {
		t.Fatalf("distill failed: %v", err)
	}
	entries, _ := os.ReadDir(draftsDir(t, dir))
	data, _ := os.ReadFile(filepath.Join(draftsDir(t, dir), entries[0].Name()))
	content := string(data)
	if idx := strings.Index(content, "description: "); idx >= 0 {
		line := content[idx:]
		if end := strings.Index(line, "\n"); end >= 0 {
			line = line[:end]
		}
		if len(line) > 200 {
			t.Errorf("description line too long (%d chars)", len(line))
		}
	} else {
		t.Error("frontmatter description missing")
	}
}
