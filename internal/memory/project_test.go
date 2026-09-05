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

	for _, d := range []string{globalDir, projectDir} {
		os.MkdirAll(d, 0755)
	}

	// Write supported project memory files
	os.WriteFile(filepath.Join(globalDir, "GGCODE.md"), []byte("global instructions"), 0644)
	os.WriteFile(filepath.Join(projectDir, "AGENTS.md"), []byte("project instructions"), 0644)

	// Monkey-patch UserHomeDir
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", filepath.Join(tmpDir, "home"))

	content, files, err := LoadProjectMemory(projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should contain global + current dir only (no parent walk)
	if !contains(content, "global instructions") {
		t.Error("missing global instructions")
	}
	if !contains(content, "project instructions") {
		t.Error("missing project instructions")
	}
	if len(files) != 2 {
		t.Errorf("expected 2 files, got %d", len(files))
	}

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

func TestLoadProjectMemory_DoesNotWalkParentDirs(t *testing.T) {
	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "project")
	subDir := filepath.Join(projectDir, "sub")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "GGCODE.md"), []byte("parent"), 0644); err != nil {
		t.Fatalf("write parent memory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "AGENTS.md"), []byte("child"), 0644); err != nil {
		t.Fatalf("write child memory: %v", err)
	}
	t.Setenv("HOME", tmpDir)

	content, files, err := LoadProjectMemory(subDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should only load from subDir itself, NOT from parent projectDir
	if contains(content, "parent") {
		t.Fatalf("should NOT load parent dir memory, got %q", content)
	}
	if !contains(content, "child") {
		t.Fatalf("expected child memory, got %q", content)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d: %v", len(files), files)
	}
}

func TestProjectMemoryFilesForPath_CurrentDirOnly(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")
	featureDir := filepath.Join(repoDir, "internal", "feature")
	for _, dir := range []string{repoDir, featureDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(repoDir, "GGCODE.md"), []byte("root"), 0644); err != nil {
		t.Fatalf("write root memory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(featureDir, "CLAUDE.md"), []byte("feature"), 0644); err != nil {
		t.Fatalf("write feature memory: %v", err)
	}

	files, err := ProjectMemoryFilesForPath(filepath.Join(featureDir, "main.go"))
	if err != nil {
		t.Fatalf("ProjectMemoryFilesForPath() error = %v", err)
	}
	// Should find the current dir's file, plus any global ~/.ggcode/ files.
	// Must NOT find files from parent directories (no parent walk).
	repoMemory := filepath.Join(repoDir, "GGCODE.md")
	for _, f := range files {
		if f == repoMemory {
			t.Fatalf("should not find parent dir memory file, got %v", files)
		}
	}
	// Verify the feature dir file is present.
	found := false
	for _, f := range files {
		if f == filepath.Join(featureDir, "CLAUDE.md") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected to find feature dir CLAUDE.md, got %v", files)
	}
}

func TestResolveProjectMemoryInitTarget_CurrentDirOnly(t *testing.T) {
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "repo", "internal", "tui")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	target, existing, err := ResolveProjectMemoryInitTarget(subDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should target the current working dir, not walk up to git root
	if target != filepath.Join(subDir, "GGCODE.md") {
		t.Fatalf("expected current-dir target, got %q", target)
	}
	if len(existing) != 0 {
		t.Fatalf("expected no existing files, got %v", existing)
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
	// #1593-A: the repo-specific path hints are module-gated - a FOREIGN
	// module (this fixture is example.com/demo) must NOT receive ggcode's
	// directory descriptions; correct silence beats wrong facts.
	if strings.Contains(content, "`cmd/ggcode/`") {
		t.Fatalf("foreign module must not receive ggcode-specific path hints, got %q", content)
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

func TestLoadProjectMemory_DoesNotScanArbitraryWorkingDirSubtrees(t *testing.T) {
	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, "home")
	workingDir := filepath.Join(homeDir, "plain")
	nestedDir := filepath.Join(workingDir, "deep", "nested")
	if err := os.MkdirAll(nestedDir, 0755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nestedDir, "AGENTS.md"), []byte("nested instructions"), 0644); err != nil {
		t.Fatalf("write nested AGENTS.md: %v", err)
	}
	t.Setenv("HOME", homeDir)

	content, files, err := LoadProjectMemory(workingDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != "" {
		t.Fatalf("expected no project memory content for non-project working dir, got %q", content)
	}
	if len(files) != 0 {
		t.Fatalf("expected no project memory files for non-project working dir, got %v", files)
	}
}

func TestBuildProjectMemoryHint_EmptyReturnsEmpty(t *testing.T) {
	if got := BuildProjectMemoryHint(nil, ""); got != "" {
		t.Fatalf("nil input should return empty string, got %q", got)
	}
	if got := BuildProjectMemoryHint([]string{}, ""); got != "" {
		t.Fatalf("empty slice should return empty string, got %q", got)
	}
}

func TestBuildProjectMemoryHint_SingleFile(t *testing.T) {
	got := BuildProjectMemoryHint([]string{"/repo/GGCODE.md"}, "/repo")
	for _, want := range []string{"## Project Memory", "GGCODE.md", "read_file"} {
		if !strings.Contains(got, want) {
			t.Fatalf("hint should contain %q, got %q", want, got)
		}
	}
}

func TestBuildProjectMemoryHint_MultipleFiles(t *testing.T) {
	got := BuildProjectMemoryHint([]string{
		"/repo/GGCODE.md",
		"/repo/CLAUDE.md",
		"/repo/AGENTS.md",
	}, "/repo")
	for _, want := range []string{"GGCODE.md", "CLAUDE.md", "AGENTS.md"} {
		if !strings.Contains(got, want) {
			t.Fatalf("hint should contain %q, got %q", want, got)
		}
	}
}

func TestBuildProjectMemoryHint_DeduplicatesByBaseName(t *testing.T) {
	// Two local files with the same base name in the same dir are deduplicated.
	got := BuildProjectMemoryHint([]string{
		"/repo/GGCODE.md",
		"/repo/GGCODE.md",
	}, "/repo")
	count := strings.Count(got, "GGCODE.md")
	if count != 1 {
		t.Fatalf("expected GGCODE.md to appear once (deduplicated), got %d occurrences", count)
	}
}

func TestBuildProjectMemoryHint_GlobalFileShowsFullPath(t *testing.T) {
	// Files outside the working directory should show their full path
	// so the agent can locate them via read_file.
	got := BuildProjectMemoryHint([]string{
		"/Users/test/.ggcode/GGCODE.md",
	}, "/Users/test/project")
	if !strings.Contains(got, "/Users/test/.ggcode/GGCODE.md") {
		t.Fatalf("hint should contain full path for global file, got %q", got)
	}
}

func TestLoadProjectMemory_CrossToolCompatibility(t *testing.T) {
	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Create cross-tool rules files
	if err := os.WriteFile(filepath.Join(projectDir, ".cursorrules"), []byte("cursor rules"), 0644); err != nil {
		t.Fatalf("write .cursorrules: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, ".windsurfrules"), []byte("windsurf rules"), 0644); err != nil {
		t.Fatalf("write .windsurfrules: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, ".clinerules"), []byte("cline rules"), 0644); err != nil {
		t.Fatalf("write .clinerules: %v", err)
	}

	t.Setenv("HOME", tmpDir)

	content, files, err := LoadProjectMemory(projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !contains(content, "cursor rules") {
		t.Errorf("missing cursor rules content")
	}
	if !contains(content, "windsurf rules") {
		t.Errorf("missing windsurf rules content")
	}
	if !contains(content, "cline rules") {
		t.Errorf("missing cline rules content")
	}
	if len(files) != 3 {
		t.Errorf("expected 3 files, got %d: %v", len(files), files)
	}
}

func TestLoadProjectMemory_GitHubCopilotInstructions(t *testing.T) {
	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "project")
	githubDir := filepath.Join(projectDir, ".github")
	if err := os.MkdirAll(githubDir, 0755); err != nil {
		t.Fatalf("mkdir .github: %v", err)
	}

	if err := os.WriteFile(filepath.Join(githubDir, "copilot-instructions.md"), []byte("copilot instructions"), 0644); err != nil {
		t.Fatalf("write copilot-instructions.md: %v", err)
	}

	t.Setenv("HOME", tmpDir)

	content, files, err := LoadProjectMemory(projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !contains(content, "copilot instructions") {
		t.Errorf("missing copilot-instructions.md content")
	}
	if len(files) != 1 {
		t.Errorf("expected 1 file, got %d: %v", len(files), files)
	}
}

func TestLoadProjectMemory_PrimaryOverridesCompatibility(t *testing.T) {
	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	os.WriteFile(filepath.Join(projectDir, "GGCODE.md"), []byte("ggcode-primary"), 0644)
	os.WriteFile(filepath.Join(projectDir, ".cursorrules"), []byte("cursor-compat"), 0644)

	t.Setenv("HOME", tmpDir)

	content, files, err := LoadProjectMemory(projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Primary files should appear first in the list
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d: %v", len(files), files)
	}
	if filepath.Base(files[0]) != "GGCODE.md" {
		t.Errorf("expected GGCODE.md first, got %s", files[0])
	}
	if filepath.Base(files[1]) != ".cursorrules" {
		t.Errorf("expected .cursorrules second, got %s", files[1])
	}

	// Both contents should be present
	if !contains(content, "ggcode-primary") || !contains(content, "cursor-compat") {
		t.Errorf("missing expected content: %q", content)
	}

	// Primary should appear before compatibility in merged content
	ggcodeIdx := strings.Index(content, "ggcode-primary")
	cursorIdx := strings.Index(content, "cursor-compat")
	if ggcodeIdx > cursorIdx {
		t.Errorf("primary content should appear before compatibility content")
	}
}

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}

// TestIssue1615_HtmlBannerNotSummary pins #1615: README banner HTML
// paragraphs must not become the project Summary.
func TestIssue1615_HtmlBannerNotSummary(t *testing.T) {
	dir := t.TempDir()
	readme := "# ggcode\n\n<p align=\"center\">\n<img src=\"banner.png\" alt=\"banner\">\n</p>\n\nAn AI coding agent for the terminal.\n"
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(readme), 0o644); err != nil {
		t.Fatal(err)
	}
	got := detectReadmeSummary(dir)
	if strings.Contains(got, "<") || strings.Contains(got, "img") {
		t.Fatalf("HTML banner leaked into summary: %q", got)
	}
	if !strings.Contains(got, "An AI coding agent") {
		t.Fatalf("real description must win, got %q", got)
	}
}
