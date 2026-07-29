package agentruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildProjectOverview_Basic(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "internal", "agent"))
	mustMkdir(t, filepath.Join(root, "internal", "tool"))
	mustMkdir(t, filepath.Join(root, "docs"))
	mustWrite(t, filepath.Join(root, "main.go"), "package main\n")
	mustWrite(t, filepath.Join(root, "go.mod"), "module example\n")

	out := BuildProjectOverview(root)
	if out == "" {
		t.Fatal("expected non-empty overview")
	}
	for _, want := range []string{"internal/", "  agent/", "  tool/", "docs/", "main.go", "go.mod"} {
		if !strings.Contains(out, want) {
			t.Errorf("overview missing %q:\n%s", want, out)
		}
	}
	// At the root, key manifest files sort before directories so they
	// survive the line budget.
	if strings.Index(out, "go.mod") > strings.Index(out, "internal/") {
		t.Errorf("expected root files before directories:\n%s", out)
	}
}

func TestBuildProjectOverview_SkipsNoiseAndHidden(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "node_modules", "pkg"))
	mustMkdir(t, filepath.Join(root, "vendor", "lib"))
	mustMkdir(t, filepath.Join(root, ".git", "objects"))
	mustMkdir(t, filepath.Join(root, ".ggcode"))
	mustMkdir(t, filepath.Join(root, "src"))
	mustWrite(t, filepath.Join(root, ".env"), "SECRET=1\n")

	out := BuildProjectOverview(root)
	for _, unwanted := range []string{"node_modules", "vendor", ".git", ".ggcode", ".env", "pkg", "objects"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("overview should omit %q:\n%s", unwanted, out)
		}
	}
	if !strings.Contains(out, "src/") {
		t.Errorf("overview missing src/:\n%s", out)
	}
}

func TestBuildProjectOverview_DepthLimit(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "a", "b", "c"))
	mustWrite(t, filepath.Join(root, "a", "b", "c", "deep.go"), "package c\n")

	out := BuildProjectOverview(root)
	if !strings.Contains(out, "  b/") {
		t.Errorf("expected depth-2 dir b/ listed:\n%s", out)
	}
	if strings.Contains(out, "c/") || strings.Contains(out, "deep.go") {
		t.Errorf("expected depth >2 entries omitted:\n%s", out)
	}
}

func TestBuildProjectOverview_FilesOnlyAtRoot(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "pkg", "sub"))
	// Many files inside a subdirectory must not consume the line budget —
	// only directory names are shown below the root.
	for i := 0; i < 30; i++ {
		mustWrite(t, filepath.Join(root, "pkg", strings.Repeat("f", i+1)+".go"), "package pkg\n")
	}
	mustWrite(t, filepath.Join(root, "go.mod"), "module example\n")

	out := BuildProjectOverview(root)
	if !strings.Contains(out, "pkg/") || !strings.Contains(out, "  sub/") {
		t.Errorf("expected pkg/ and sub/ dirs:\n%s", out)
	}
	if !strings.Contains(out, "go.mod") {
		t.Errorf("expected root file go.mod listed:\n%s", out)
	}
	if strings.Contains(out, ".go\n") || strings.Contains(out, "f.go") {
		t.Errorf("expected subdirectory files omitted:\n%s", out)
	}
	if strings.Contains(out, "truncated") {
		t.Errorf("30 subdir files should not cause truncation:\n%s", out)
	}
}

func TestBuildProjectOverview_Truncates(t *testing.T) {
	root := t.TempDir()
	// Create more entries than the cap at depth 0.
	for i := 0; i < overviewMaxEntries+10; i++ {
		mustWrite(t, filepath.Join(root, "file"+string(rune('a'+i%26))+string(rune('a'+i/26))+".txt"), "x")
	}
	out := BuildProjectOverview(root)
	if !strings.Contains(out, "truncated") {
		t.Errorf("expected truncation marker:\n%s", out)
	}
	lines := strings.Count(out, "\n")
	if lines > overviewMaxEntries+1 {
		t.Errorf("expected at most %d lines, got %d", overviewMaxEntries+1, lines)
	}
}

func TestBuildProjectOverview_EmptyAndMissing(t *testing.T) {
	if out := BuildProjectOverview(filepath.Join(t.TempDir(), "does-not-exist")); out != "" {
		t.Errorf("expected empty for missing dir, got %q", out)
	}
	if out := BuildProjectOverview(t.TempDir()); out != "" {
		t.Errorf("expected empty for empty dir, got %q", out)
	}
}

func TestProjectOverviewSection(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "pkg"))
	sec := projectOverviewSection(root)
	if !strings.Contains(sec, "## Project layout") || !strings.Contains(sec, "pkg/") {
		t.Errorf("unexpected section:\n%s", sec)
	}
	if got := projectOverviewSection(""); got != "" {
		t.Errorf("expected empty section for empty dir, got %q", got)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
