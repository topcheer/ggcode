package knight

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSelfReflectionEmptyKnight(t *testing.T) {
	dir := t.TempDir()
	k := &Knight{projDir: dir, index: NewSkillIndex(filepath.Join(dir, "missing-home"), filepath.Join(dir, ".ggcode", "skills"))}
	report, err := k.RunSelfReflection(context.Background(), 0)
	if err != nil {
		t.Fatalf("self-reflection: %v", err)
	}
	if report.Window != 7*24*time.Hour {
		t.Fatalf("expected default window applied, got %s", report.Window)
	}
	if !strings.Contains(report.MetaLesson(), "active=0") {
		t.Fatalf("expected zeroed lesson, got %q", report.MetaLesson())
	}
}

func TestSelfReflectionRecordsToMemory(t *testing.T) {
	dir := t.TempDir()
	k := &Knight{projDir: dir, index: NewSkillIndex(filepath.Join(dir, "missing-home"), filepath.Join(dir, ".ggcode", "skills"))}
	if _, err := k.RunSelfReflection(context.Background(), 24*time.Hour); err != nil {
		t.Fatalf("run: %v", err)
	}
	mem, err := k.RecentSemanticMemory(5)
	if err != nil {
		t.Fatalf("recent memory: %v", err)
	}
	if len(mem) == 0 {
		t.Fatal("expected self-reflection to be recorded in semantic memory")
	}
	if mem[0].Kind != "self-reflection" {
		t.Fatalf("expected kind=self-reflection, got %q", mem[0].Kind)
	}
}
