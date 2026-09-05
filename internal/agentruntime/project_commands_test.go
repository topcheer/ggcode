package agentruntime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectLintCommand_MakefileLintTarget(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Makefile", "lint:\n\tgolangci-lint run\n")

	got := detectLintCommand(dir)
	if got != "make lint" {
		t.Errorf("detectLintCommand() = %q, want %q", got, "make lint")
	}
}

func TestDetectLintCommand_MakefileVetTarget(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Makefile", "vet:\n\tgo vet ./...\n")

	got := detectLintCommand(dir)
	if got != "make vet" {
		t.Errorf("detectLintCommand() = %q, want %q", got, "make vet")
	}
}

func TestDetectLintCommand_GolangciConfig(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".golangci.yml", "linters:\n  enable:\n    - errcheck\n")

	got := detectLintCommand(dir)
	if got != "golangci-lint run" {
		t.Errorf("detectLintCommand() = %q, want %q", got, "golangci-lint run")
	}
}

func TestDetectLintCommand_GoModFallback(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/test\n\ngo 1.21\n")

	got := detectLintCommand(dir)
	if got != "go vet ./..." {
		t.Errorf("detectLintCommand() = %q, want %q", got, "go vet ./...")
	}
}

func TestDetectLintCommand_ESLint(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".eslintrc.json", `{"rules": {}}`)

	got := detectLintCommand(dir)
	if got != "eslint ." {
		t.Errorf("detectLintCommand() = %q, want %q", got, "eslint .")
	}
}

func TestDetectLintCommand_Ruff(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "ruff.toml", "line-length = 88\n")

	got := detectLintCommand(dir)
	if got != "ruff check ." {
		t.Errorf("detectLintCommand() = %q, want %q", got, "ruff check .")
	}
}

func TestDetectLintCommand_None(t *testing.T) {
	dir := t.TempDir()
	got := detectLintCommand(dir)
	if got != "" {
		t.Errorf("detectLintCommand() = %q, want empty", got)
	}
}

func TestDetectFormatCommand_MakefileFmtTarget(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Makefile", "fmt:\n\tgofmt -w .\n")

	got := detectFormatCommand(dir)
	if got != "make fmt" {
		t.Errorf("detectFormatCommand() = %q, want %q", got, "make fmt")
	}
}

func TestDetectFormatCommand_MakefileFormatTarget(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Makefile", "format:\n\tprettier --write .\n")

	got := detectFormatCommand(dir)
	if got != "make format" {
		t.Errorf("detectFormatCommand() = %q, want %q", got, "make format")
	}
}

func TestDetectFormatCommand_GoModFallback(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/test\n\ngo 1.21\n")

	got := detectFormatCommand(dir)
	if got != "gofmt -w ." {
		t.Errorf("detectFormatCommand() = %q, want %q", got, "gofmt -w .")
	}
}

func TestDetectFormatCommand_Prettier(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".prettierrc", `{"semi": true}`)

	got := detectFormatCommand(dir)
	if got != "prettier --write ." {
		t.Errorf("detectFormatCommand() = %q, want %q", got, "prettier --write .")
	}
}

func TestDetectFormatCommand_None(t *testing.T) {
	dir := t.TempDir()
	got := detectFormatCommand(dir)
	if got != "" {
		t.Errorf("detectFormatCommand() = %q, want empty", got)
	}
}

func TestDetectProjectCommands_IncludesLintAndFormat(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Makefile", "build:\n\tgo build ./...\nlint:\n\tgolangci-lint run\nfmt:\n\tgofmt -w .\n")

	cmds := detectProjectCommands(dir)
	if cmds.verify != "make build" {
		t.Errorf("verify = %q, want %q", cmds.verify, "make build")
	}
	if cmds.lint != "make lint" {
		t.Errorf("lint = %q, want %q", cmds.lint, "make lint")
	}
	if cmds.format != "make fmt" {
		t.Errorf("format = %q, want %q", cmds.format, "make fmt")
	}
}

func TestDetectProjectCommands_GoModWithLintAndFormat(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/test\n\ngo 1.21\n")

	cmds := detectProjectCommands(dir)
	if cmds.verify != "go build ./..." {
		t.Errorf("verify = %q, want %q", cmds.verify, "go build ./...")
	}
	if cmds.test != "go test ./..." {
		t.Errorf("test = %q, want %q", cmds.test, "go test ./...")
	}
	if cmds.lint != "go vet ./..." {
		t.Errorf("lint = %q, want %q", cmds.lint, "go vet ./...")
	}
	if cmds.format != "gofmt -w ." {
		t.Errorf("format = %q, want %q", cmds.format, "gofmt -w .")
	}
}

func TestProjectCommandsSection_IncludesLintAndFormat(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Makefile", "build:\n\tgo build ./...\nlint:\n\tgolangci-lint run\nfmt:\n\tgofmt -w .\n")

	section := projectCommandsSection(dir)
	if section == "" {
		t.Fatal("expected non-empty section")
	}
	for _, want := range []string{"Verify:", "Lint:", "Format:"} {
		if !contains(section, want) {
			t.Errorf("projectCommandsSection() missing %q in output:\n%s", want, section)
		}
	}
}

func TestProjectCommandsSection_LintOnlyNoBuildSystem(t *testing.T) {
	dir := t.TempDir()
	// Only a golangci config, no Makefile or go.mod
	writeFile(t, dir, ".golangci.yml", "linters:\n  enable: []\n")

	section := projectCommandsSection(dir)
	if section == "" {
		t.Fatal("expected non-empty section even with lint only")
	}
	if !contains(section, "Lint:") {
		t.Errorf("section should contain Lint:\n%s", section)
	}
}

func TestProjectCommandsSection_EmptyWhenNothing(t *testing.T) {
	dir := t.TempDir()
	section := projectCommandsSection(dir)
	if section != "" {
		t.Errorf("expected empty section for bare dir, got:\n%s", section)
	}
}

// --- helpers ---

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// Regression for #1489: Taskfile target detection used bare Contains, so
// tail-suffixed task names (e2e_test:, build-ci:) matched test:/ci: and
// injected nonexistent verify/lint/format commands into the system prompt.
func TestDetectCommands_TaskfileSuffixNoFalseHit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Taskfile.yml"),
		[]byte("version: '3'\n\ntasks:\n  e2e_test:\n    cmds: [go test ./e2e]\n  build-ci:\n    cmds: [go build]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := detectLintCommand(dir); got != "" {
		t.Fatalf("lint must not match e2e_test (no bare test: target), got %q", got)
	}
	if got := detectFormatCommand(dir); got != "" {
		t.Fatalf("format must not match, got %q", got)
	}
}

func TestDetectCommands_TaskfileExactTargetStillHits(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Taskfile.yml"),
		[]byte("version: '3'\n\ntasks:\n  test:\n    cmds: [go test ./...]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info := detectProjectCommands(dir)
	if info.verify != "task test" {
		t.Fatalf("exact test: target must still match, got verify=%q", info.verify)
	}
}
