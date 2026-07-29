package agentruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectProjectCommands_Makefile(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "Makefile"), "verify-ci:\n\tgo test ./...\n\ntest:\n\tgo test ./...\n")

	cmds := detectProjectCommands(root)
	if cmds.verify != "make verify-ci" {
		t.Errorf("expected 'make verify-ci', got %q", cmds.verify)
	}
}

func TestDetectProjectCommands_Makefile_TestTarget(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "Makefile"), "build:\n\tgo build\n\ntest:\n\tgo test\n")

	cmds := detectProjectCommands(root)
	if cmds.verify != "make test" {
		t.Errorf("expected 'make test' (first matching target), got %q", cmds.verify)
	}
}

func TestDetectProjectCommands_Makefile_NoMatch(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "Makefile"), "all:\n\techo hi\n")
	mustWrite(t, filepath.Join(root, "go.mod"), "module x\n")

	// Makefile exists but no recognized target → falls through to language detection
	cmds := detectProjectCommands(root)
	if cmds.verify != "go build ./..." {
		t.Errorf("expected fallback to go build, got %q", cmds.verify)
	}
}

func TestDetectProjectCommands_GoMod(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module example\n")

	cmds := detectProjectCommands(root)
	if cmds.verify != "go build ./..." {
		t.Errorf("expected 'go build ./...', got %q", cmds.verify)
	}
	if cmds.test != "go test ./..." {
		t.Errorf("expected 'go test ./...', got %q", cmds.test)
	}
}

func TestDetectProjectCommands_Cargo(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = \"x\"\n")

	cmds := detectProjectCommands(root)
	if cmds.verify != "cargo build" {
		t.Errorf("expected 'cargo build', got %q", cmds.verify)
	}
	if cmds.test != "cargo test" {
		t.Errorf("expected 'cargo test', got %q", cmds.test)
	}
}

func TestDetectProjectCommands_NPM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{"name":"x"}`)

	cmds := detectProjectCommands(root)
	if cmds.verify != "npm run build" {
		t.Errorf("expected 'npm run build', got %q", cmds.verify)
	}
	if cmds.test != "npm test" {
		t.Errorf("expected 'npm test', got %q", cmds.test)
	}
}

func TestDetectProjectCommands_VerifyScript(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "scripts", "dev"))
	mustWrite(t, filepath.Join(root, "scripts", "dev", "verify-ci.sh"), "#!/bin/bash\n")

	cmds := detectProjectCommands(root)
	if cmds.verify != "bash scripts/dev/verify-ci.sh" {
		t.Errorf("expected 'bash scripts/dev/verify-ci.sh', got %q", cmds.verify)
	}
}

func TestDetectProjectCommands_Justfile(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "justfile"), "test:\n\techo test\n")

	cmds := detectProjectCommands(root)
	if !strings.HasPrefix(cmds.verify, "just") {
		t.Errorf("expected just command, got %q", cmds.verify)
	}
}

func TestDetectProjectCommands_EmptyAndMissing(t *testing.T) {
	if cmds := detectProjectCommands(""); cmds.verify != "" || cmds.test != "" {
		t.Errorf("expected empty for empty dir, got %+v", cmds)
	}
	if cmds := detectProjectCommands(filepath.Join(t.TempDir(), "nope")); cmds.verify != "" {
		t.Errorf("expected empty for missing dir, got %+v", cmds)
	}
	// Empty dir with no build markers
	if cmds := detectProjectCommands(t.TempDir()); cmds.verify != "" || cmds.test != "" {
		t.Errorf("expected empty for dir with no markers, got %+v", cmds)
	}
}

func TestProjectCommandsSection_GoProject(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module x\n")

	section := projectCommandsSection(root)
	if !strings.Contains(section, "## Project commands") {
		t.Errorf("expected section header, got: %s", section)
	}
	if !strings.Contains(section, "go build ./...") {
		t.Errorf("expected go build command, got: %s", section)
	}
}

func TestProjectCommandsSection_NoProject(t *testing.T) {
	root := t.TempDir()
	section := projectCommandsSection(root)
	if section != "" {
		t.Errorf("expected empty section for no-marker dir, got %q", section)
	}
}

func TestProjectCommandsSection_EmptyDir(t *testing.T) {
	if section := projectCommandsSection(""); section != "" {
		t.Errorf("expected empty section for empty dir, got %q", section)
	}
}

func TestCmdHasMakeTarget(t *testing.T) {
	content := "verify-ci:\n\tgo test\n\n# build: comment\nbuild:\n\techo build\n"

	tests := []struct {
		target string
		want   bool
	}{
		{"verify-ci", true},
		{"build", true},
		{"test", false},
		{"nonexistent", false},
	}
	for _, tt := range tests {
		if got := cmdHasMakeTarget(content, tt.target); got != tt.want {
			t.Errorf("cmdHasMakeTarget(%q) = %v, want %v", tt.target, got, tt.want)
		}
	}
}

func TestCmdFileExists(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.txt")
	if cmdFileExists(path) {
		t.Error("expected false for non-existent file")
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !cmdFileExists(path) {
		t.Error("expected true for existing file")
	}
}

func TestDetectProjectCommands_Priority(t *testing.T) {
	// Makefile takes priority over go.mod
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "Makefile"), "ci:\n\techo ci\n")
	mustWrite(t, filepath.Join(root, "go.mod"), "module x\n")

	cmds := detectProjectCommands(root)
	if cmds.verify != "make ci" {
		t.Errorf("expected Makefile priority (make ci), got %q", cmds.verify)
	}
}

func TestDetectProjectCommands_Gradle(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "build.gradle"), "plugins { id 'java' }\n")

	cmds := detectProjectCommands(root)
	if cmds.verify != "gradle build" {
		t.Errorf("expected 'gradle build', got %q", cmds.verify)
	}
}

func TestDetectProjectCommands_Maven(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "pom.xml"), "<project></project>\n")

	cmds := detectProjectCommands(root)
	if cmds.verify != "mvn compile" {
		t.Errorf("expected 'mvn compile', got %q", cmds.verify)
	}
}

func TestDetectProjectCommands_CMake(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "CMakeLists.txt"), "cmake_minimum_required(VERSION 3.0)\n")

	cmds := detectProjectCommands(root)
	if cmds.verify != "cmake --build build" {
		t.Errorf("expected 'cmake --build build', got %q", cmds.verify)
	}
}
