package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectProjectProfile_Go(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, dir, "go.mod", "module example.com/test\n\ngo 1.21\n")

	profile := DetectProjectProfile(dir)
	if profile == nil {
		t.Fatal("expected non-nil profile for Go project")
	}
	if !sliceContains(profile.Languages, "Go") {
		t.Errorf("expected Go in languages, got %v", profile.Languages)
	}
	if profile.BuildCommand == "" {
		t.Error("expected non-empty build command")
	}
	if !strings.Contains(profile.TestCommand, "go test") {
		t.Errorf("expected go test in test command, got %s", profile.TestCommand)
	}
}

func TestDetectProjectProfile_GoWithMakefileTags(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, dir, "go.mod", "module example.com/test\n\ngo 1.21\n")
	mustWriteFile(t, dir, "Makefile", "TAGS := goolm\nbuild:\n\tgo build -tags $(TAGS) ./...\n")

	profile := DetectProjectProfile(dir)
	if profile == nil {
		t.Fatal("expected non-nil profile")
	}
	if !strings.Contains(profile.BuildCommand, "-tags goolm") {
		t.Errorf("expected -tags goolm in build command, got %s", profile.BuildCommand)
	}
	if !strings.Contains(profile.TestCommand, "-tags goolm") {
		t.Errorf("expected -tags goolm in test command, got %s", profile.TestCommand)
	}
}

func TestDetectProjectProfile_NodeReact(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, dir, "package.json", `{
		"name": "test-app",
		"dependencies": {
			"react": "^18.0.0"
		},
		"scripts": {
			"build": "vite build",
			"test": "vitest",
			"lint": "eslint ."
		}
	}`)

	profile := DetectProjectProfile(dir)
	if profile == nil {
		t.Fatal("expected non-nil profile")
	}
	if !sliceContains(profile.Languages, "JavaScript/TypeScript") {
		t.Errorf("expected JavaScript/TypeScript, got %v", profile.Languages)
	}
	if !sliceContains(profile.Frameworks, "React") {
		t.Errorf("expected React framework, got %v", profile.Frameworks)
	}
	if profile.LintCommand != "npm run lint" {
		t.Errorf("expected npm run lint, got %s", profile.LintCommand)
	}
}

func TestDetectProjectProfile_Rust(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, dir, "Cargo.toml", "[package]\nname = \"test\"\nversion = \"0.1.0\"\n\n[dependencies]\ntokio = \"1\"\n")

	profile := DetectProjectProfile(dir)
	if profile == nil {
		t.Fatal("expected non-nil profile")
	}
	if !sliceContains(profile.Languages, "Rust") {
		t.Errorf("expected Rust, got %v", profile.Languages)
	}
	if profile.BuildCommand != "cargo build" {
		t.Errorf("expected cargo build, got %s", profile.BuildCommand)
	}
	if profile.LintCommand != "cargo clippy" {
		t.Errorf("expected cargo clippy, got %s", profile.LintCommand)
	}
}

func TestDetectProjectProfile_Python(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, dir, "pyproject.toml", "[project]\nname = \"test\"\nversion = \"0.1.0\"\n")

	profile := DetectProjectProfile(dir)
	if profile == nil {
		t.Fatal("expected non-nil profile")
	}
	if !sliceContains(profile.Languages, "Python") {
		t.Errorf("expected Python, got %v", profile.Languages)
	}
}

func TestDetectProjectProfile_Monorepo(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, dir, "package.json", `{
		"name": "monorepo",
		"workspaces": ["packages/*"]
	}`)

	profile := DetectProjectProfile(dir)
	if profile == nil {
		t.Fatal("expected non-nil profile")
	}
	if !sliceContains(profile.Frameworks, "monorepo") {
		t.Errorf("expected monorepo framework, got %v", profile.Frameworks)
	}
}

func TestDetectProjectProfile_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	profile := DetectProjectProfile(dir)
	if profile != nil {
		t.Error("expected nil profile for empty directory")
	}
}

func TestDetectProjectProfile_EmptyWorkingDir(t *testing.T) {
	profile := DetectProjectProfile("")
	if profile != nil {
		t.Error("expected nil for empty working dir")
	}
}

func TestFormatForSystemPrompt(t *testing.T) {
	profile := &ProjectProfile{
		Languages:    []string{"Go"},
		BuildSystem:  "Make",
		BuildCommand: "go build -tags goolm ./...",
		TestCommand:  "go test -tags goolm ./...",
		KeyFiles:     []string{"go.mod", "Makefile"},
	}
	text := profile.FormatForSystemPrompt()
	if !strings.Contains(text, "Go") {
		t.Errorf("expected Go in output, got %s", text)
	}
	if !strings.Contains(text, "go build -tags goolm") {
		t.Errorf("expected build command in output, got %s", text)
	}
}

func TestFormatForSystemPrompt_Nil(t *testing.T) {
	var profile *ProjectProfile
	text := profile.FormatForSystemPrompt()
	if text != "" {
		t.Errorf("expected empty string for nil profile, got %s", text)
	}
}

func TestDetectProfileText(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, dir, "go.mod", "module test\n\ngo 1.21\n")

	text := detectProfileText(dir)
	if text == "" {
		t.Error("expected non-empty profile text")
	}
	if !strings.Contains(text, "Project Profile") {
		t.Errorf("expected 'Project Profile' header, got %s", text)
	}
}

func TestDetectProfileText_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	text := detectProfileText(dir)
	if text != "" {
		t.Errorf("expected empty text for empty dir, got %s", text)
	}
}

func TestBuildSystemPrompt_WithProjectProfile(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, dir, "go.mod", "module test\n\ngo 1.21\n")

	prompt := BuildSystemPrompt("", dir, "en", []string{"read_file"}, "", nil, nil)
	if !strings.Contains(prompt, "Project Profile") {
		t.Errorf("expected project profile in system prompt, got tail: %s", prompt[len(prompt)-500:])
	}
	if !strings.Contains(prompt, "Go") {
		t.Errorf("expected Go language in system prompt")
	}
}

// --- helpers ---

func mustWriteFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
