package agent

import (
	"strings"
	"testing"
)

func TestChurnState_NoEdits(t *testing.T) {
	s := newChurnState()
	if g := s.check(); g != "" {
		t.Fatalf("expected empty guidance with no edits, got: %s", g)
	}
}

func TestChurnState_BelowThreshold(t *testing.T) {
	s := newChurnState()
	s.recordEdit([]string{"foo.go"})
	s.recordEdit([]string{"foo.go"})
	if g := s.check(); g != "" {
		t.Fatalf("expected empty guidance below threshold, got: %s", g)
	}
}

func TestChurnState_AtThreshold(t *testing.T) {
	s := newChurnState()
	s.recordEdit([]string{"foo.go"})
	s.recordEdit([]string{"foo.go"})
	s.recordEdit([]string{"foo.go"})
	g := s.check()
	if g == "" {
		t.Fatal("expected guidance at threshold (3 edits)")
	}
	if !strings.Contains(g, "foo.go") {
		t.Errorf("guidance should mention the churned file, got: %s", g)
	}
	if !strings.Contains(g, "3 edits") {
		t.Errorf("guidance should show edit count, got: %s", g)
	}
}

func TestChurnState_FiresOnlyOnce(t *testing.T) {
	s := newChurnState()
	s.recordEdit([]string{"a.go"})
	s.recordEdit([]string{"a.go"})
	s.recordEdit([]string{"a.go"})
	g1 := s.check()
	if g1 == "" {
		t.Fatal("expected guidance on first check")
	}
	s.recordEdit([]string{"a.go"})
	g2 := s.check()
	if g2 != "" {
		t.Fatal("expected no guidance after already fired")
	}
}

func TestChurnState_Reset(t *testing.T) {
	s := newChurnState()
	s.recordEdit([]string{"a.go"})
	s.recordEdit([]string{"a.go"})
	s.recordEdit([]string{"a.go"})
	g := s.check()
	if g == "" {
		t.Fatal("expected guidance before reset")
	}
	s.reset()
	if g := s.check(); g != "" {
		t.Fatal("expected no guidance after reset")
	}
	// Should fire again after new edits post-reset.
	s.recordEdit([]string{"b.go"})
	s.recordEdit([]string{"b.go"})
	s.recordEdit([]string{"b.go"})
	g = s.check()
	if g == "" {
		t.Fatal("expected guidance after reset + new edits")
	}
}

func TestChurnState_MultipleChurnedFiles(t *testing.T) {
	s := newChurnState()
	// Two files churned
	for i := 0; i < 4; i++ {
		s.recordEdit([]string{"a.go"})
	}
	for i := 0; i < 3; i++ {
		s.recordEdit([]string{"b.go"})
	}
	g := s.check()
	if g == "" {
		t.Fatal("expected guidance with multiple churned files")
	}
	if !strings.Contains(g, "a.go") {
		t.Errorf("guidance should mention a.go (highest count), got: %s", g)
	}
	if !strings.Contains(g, "b.go") {
		t.Errorf("guidance should mention b.go, got: %s", g)
	}
}

func TestChurnState_MultiFileEdit(t *testing.T) {
	s := newChurnState()
	// multi_file_edit passes multiple paths
	paths := []string{"x.go", "y.go"}
	s.recordEdit(paths)
	s.recordEdit(paths)
	s.recordEdit(paths)
	g := s.check()
	if g == "" {
		t.Fatal("expected guidance for multi-file edit churn")
	}
	if !strings.Contains(g, "x.go") {
		t.Errorf("should mention x.go, got: %s", g)
	}
	if !strings.Contains(g, "y.go") {
		t.Errorf("should mention y.go, got: %s", g)
	}
}

func TestChurnState_EmptyPathsIgnored(t *testing.T) {
	s := newChurnState()
	s.recordEdit([]string{"", "", ""})
	if g := s.check(); g != "" {
		t.Fatal("empty paths should not count as edits")
	}
}

func TestChurnState_DifferentFilesNoChurn(t *testing.T) {
	s := newChurnState()
	s.recordEdit([]string{"a.go"})
	s.recordEdit([]string{"b.go"})
	s.recordEdit([]string{"c.go"})
	if g := s.check(); g != "" {
		t.Fatal("editing different files should not trigger churn")
	}
}

func TestChurnState_MaxTrackedEviction(t *testing.T) {
	s := newChurnState()
	// Fill beyond churnMaxTracked to test eviction logic.
	for i := 0; i < churnMaxTracked+10; i++ {
		s.recordEdit([]string{string(rune('a'+i%26)) + ".go"})
	}
	// Should not panic and should still work.
	_ = s.check()
}

func TestChurnState_GuidanceContent(t *testing.T) {
	s := newChurnState()
	for i := 0; i < 5; i++ {
		s.recordEdit([]string{"main.go"})
	}
	g := s.check()
	if !strings.Contains(g, "Assumption Invalidation") {
		t.Errorf("guidance should mention assumption invalidation, got: %s", g)
	}
	if !strings.Contains(g, "re-read") {
		t.Errorf("guidance should suggest re-reading, got: %s", g)
	}
	if !strings.Contains(g, "root cause") {
		t.Errorf("guidance should mention root cause, got: %s", g)
	}
}

func TestChurnState_TopNFilesOnly(t *testing.T) {
	s := newChurnState()
	// Create more churned files than churnMaxWarningFiles
	for i := 0; i < churnMaxWarningFiles+2; i++ {
		fname := string(rune('a'+i)) + ".go"
		for j := 0; j < churnThreshold; j++ {
			s.recordEdit([]string{fname})
		}
	}
	g := s.check()
	if g == "" {
		t.Fatal("expected guidance")
	}
	// Count how many files are listed (look for ".go" in guidance)
	count := strings.Count(g, ".go")
	// Should be capped at churnMaxWarningFiles (plus any in surrounding text)
	if count > churnMaxWarningFiles+2 {
		t.Errorf("too many files listed: %d (max should be ~%d)", count, churnMaxWarningFiles)
	}
}
