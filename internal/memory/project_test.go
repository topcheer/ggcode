package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadProjectMemory(t *testing.T) {
	// Create temp dirs
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "home", ".ggcode")
	projectDir := filepath.Join(tmpDir, "project")
	subDir := filepath.Join(projectDir, "sub")

	for _, d := range []string{globalDir, projectDir, subDir} {
		os.MkdirAll(d, 0755)
	}

	// Write supported project memory files
	os.WriteFile(filepath.Join(globalDir, "GGCODE.md"), []byte("global instructions"), 0644)
	os.WriteFile(filepath.Join(projectDir, "AGENTS.md"), []byte("project instructions"), 0644)
	os.WriteFile(filepath.Join(subDir, "CLAUDE.md"), []byte("sub instructions"), 0644)

	// Monkey-patch UserHomeDir
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", filepath.Join(tmpDir, "home"))

	content, files, err := LoadProjectMemory(subDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should contain all three
	if !contains(content, "global instructions") {
		t.Error("missing global instructions")
	}
	if !contains(content, "project instructions") {
		t.Error("missing project instructions")
	}
	if !contains(content, "sub instructions") {
		t.Error("missing sub instructions")
	}
	if len(files) != 3 {
		t.Errorf("expected 3 files, got %d", len(files))
	}

	// Restore
	_ = os.Setenv("HOME", origHome)
}

func TestLoadProjectMemory_MultipleNamesInOneDir(t *testing.T) {
	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	os.WriteFile(filepath.Join(projectDir, "GGCODE.md"), []byte("ggcode"), 0644)
	os.WriteFile(filepath.Join(projectDir, "AGENTS.md"), []byte("agents"), 0644)
	os.WriteFile(filepath.Join(projectDir, "COPILOT.md"), []byte("copilot"), 0644)

	t.Setenv("HOME", tmpDir)

	content, files, err := LoadProjectMemory(projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(content, "ggcode") || !contains(content, "agents") || !contains(content, "copilot") {
		t.Fatalf("expected all supported filenames to be loaded, got %q", content)
	}
	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %d", len(files))
	}
	if files[0] != filepath.Join(projectDir, "GGCODE.md") || files[1] != filepath.Join(projectDir, "AGENTS.md") || files[2] != filepath.Join(projectDir, "COPILOT.md") {
		t.Fatalf("unexpected file order: %v", files)
	}
}

func TestResolveProjectMemoryInitTarget_UsesGitRoot(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")
	subDir := filepath.Join(repoDir, "internal", "tui")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Mkdir(filepath.Join(repoDir, ".git"), 0755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}

	target, existing, err := ResolveProjectMemoryInitTarget(subDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target != filepath.Join(repoDir, "GGCODE.md") {
		t.Fatalf("expected repo-root target, got %q", target)
	}
	if len(existing) != 0 {
		t.Fatalf("expected no existing files, got %v", existing)
	}
}

func TestResolveProjectMemoryInitTarget_PrefersExistingProjectMemoryDir(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")
	subDir := filepath.Join(repoDir, "docs", "plans")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "AGENTS.md"), []byte("agents"), 0644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}

	target, existing, err := ResolveProjectMemoryInitTarget(subDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target != filepath.Join(repoDir, "GGCODE.md") {
		t.Fatalf("expected GGCODE.md target in existing project memory dir, got %q", target)
	}
	if len(existing) != 1 || existing[0] != filepath.Join(repoDir, "AGENTS.md") {
		t.Fatalf("unexpected existing files: %v", existing)
	}
}

func TestGenerateProjectMemory_UsesCurrentRepoFacts(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(tmpDir, ".git"), 0755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tmpDir, "cmd", "ggcode"), 0755); err != nil {
		t.Fatalf("mkdir cmd: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tmpDir, "internal", "tui"), 0755); err != nil {
		t.Fatalf("mkdir internal/tui: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tmpDir, "npm"), 0755); err != nil {
		t.Fatalf("mkdir npm: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "README.md"), []byte("# demo\n\nA terminal-based AI coding agent.\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module example.com/demo\n\nrequire github.com/charmbracelet/bubbletea v1.0.0\n"), 0644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "Makefile"), []byte("build:\n\ttest\n\ntest:\n\tgo test ./...\n\nlint:\n\tgo vet ./...\n"), 0644); err != nil {
		t.Fatalf("write Makefile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "ggcode.example.yaml"), []byte("vendor: zai\nendpoint: x\nvendors:\n  zai: {}\n"), 0644); err != nil {
		t.Fatalf("write example config: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tmpDir, "internal", "permission"), 0755); err != nil {
		t.Fatalf("mkdir permission: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "internal", "permission", "mode.go"), []byte("const AutopilotMode = 1"), 0644); err != nil {
		t.Fatalf("write mode.go: %v", err)
	}

	content, err := GenerateProjectMemory(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(content, "example.com/demo") {
		t.Fatalf("expected module in generated content, got %q", content)
	}
	if !strings.Contains(content, "A terminal-based AI coding agent.") {
		t.Fatalf("expected README summary in generated content, got %q", content)
	}
	if !strings.Contains(content, "`cmd/ggcode/`") {
		t.Fatalf("expected important paths in generated content, got %q", content)
	}
	if !strings.Contains(content, "`go test ./... && go vet ./...`") {
		t.Fatalf("expected validation command in generated content, got %q", content)
	}
	if !strings.Contains(content, "vendor` / `endpoint` / `vendors`") {
		t.Fatalf("expected config convention in generated content, got %q", content)
	}
}

func TestLoadProjectMemory_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	// Set home to a dir without .ggcode
	t.Setenv("HOME", tmpDir)

	content, files, err := LoadProjectMemory(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != "" {
		t.Errorf("expected empty content, got: %q", content)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 files, got %d", len(files))
	}
}

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}
