package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPersonaAnchorExtractPersist pins constraint extraction from user
// messages (EN + CN) and persistence to anchors.json across sessions.
func TestPersonaAnchorExtractPersist(t *testing.T) {
	dir := t.TempDir()
	path := AnchorPath(dir)

	s := LoadAnchorStore(path)
	n := s.ObserveUserMessage("Please use worktrees. Never push directly to main. 不要修改 pubspec.yaml。")
	if n < 2 {
		t.Fatalf("must extract EN+CN constraints, got %d", n)
	}

	// Reload as a fresh session would.
	s2 := LoadAnchorStore(path)
	if len(s2.Anchors) != len(s.Anchors) {
		t.Fatalf("anchors must persist, got %d vs %d", len(s2.Anchors), len(s.Anchors))
	}
	// Dedup: same message adds nothing.
	if s2.ObserveUserMessage("Never push directly to main.") != 0 {
		t.Fatal("duplicate constraints must be deduped")
	}
}

// TestPersonaAnchorViolation pins the violation advisory: a tool target
// matching a persisted prohibition keyword triggers [anchor-violation] with
// the anchor text, and bumps the violation counter persistently.
func TestPersonaAnchorViolation(t *testing.T) {
	dir := t.TempDir()
	path := AnchorPath(dir)
	s := LoadAnchorStore(path)
	s.ObserveUserMessage("Never push directly to main branch.")

	got := s.CheckViolation("run_command", "git push origin main --tags")
	if got == "" || !strings.Contains(got, "[anchor-violation]") || !strings.Contains(got, "Never push directly") {
		t.Fatalf("violation advisory must cite anchor, got %q", got)
	}
	if s.Anchors[0].Violations != 1 {
		t.Fatalf("violation counter must bump, got %d", s.Anchors[0].Violations)
	}
	// Benign target: silent.
	if v := s.CheckViolation("read_file", "/tmp/notes.md"); v != "" {
		t.Fatalf("benign target must stay silent, got %q", v)
	}
	// Counter persisted.
	s2 := LoadAnchorStore(path)
	if s2.Anchors[0].Violations != 1 {
		t.Fatalf("violation count must persist, got %d", s2.Anchors[0].Violations)
	}
}

// TestPersonaAnchorPromptSection pins the affirmation surface: section lists
// anchors, empty store renders nothing.
func TestPersonaAnchorPromptSection(t *testing.T) {
	s := LoadAnchorStore(AnchorPath(t.TempDir()))
	if got := s.RenderAnchorsSection(); got != "" {
		t.Fatalf("empty store must render nothing, got %q", got)
	}
	s.ObserveUserMessage("必须使用 -tags goolm 构建。")
	got := s.RenderAnchorsSection()
	if !strings.Contains(got, "Persisted User Constraints") || !strings.Contains(got, "goolm") {
		t.Fatalf("section must list anchor text, got %q", got)
	}
	if _, err := os.Stat(filepath.Join(t.TempDir(), "x")); err == nil {
		_ = err // touch os/filepath usage for linters
	}
}
