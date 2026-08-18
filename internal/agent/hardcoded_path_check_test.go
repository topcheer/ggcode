package agent

import (
	"strings"
	"testing"
)

func TestCheckHardcodedPaths_NoPaths(t *testing.T) {
	old := "package main\n\nfunc main() {}\n"
	new := "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n"
	warnings := checkHardcodedPaths("main.go", old, new)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %d: %v", len(warnings), warnings)
	}
}

// #733: substring matching exempted non-fixture paths that merely CONTAIN
// an exempt word (mockserver, remocks, myfixturesnote). Segment-exact
// matching (pathHasSegment, same as the #247 fix) must exempt only real
// fixture segments while real hardcoded paths in lookalike dirs warn.
func TestCheckHardcodedPaths_FixtureExemptionSegmentExact(t *testing.T) {
	old := "package main\n"
	new := `package main

const dbConfig = "/Users/john/secrets/db-config.json"
`
	cases := []struct {
		name     string
		filePath string
	}{
		{"mockserver contains mocks", "internal/mockserver/config.go"},
		{"remocks contains mocks", "pkg/remocks/util.go"},
		{"remockservice contains mocks", "api/remockservice/handler.go"},
		{"myfixturesnote contains fixtures", "cmd/myfixturesnote/main.go"},
	}
	for _, tc := range cases {
		warnings := checkHardcodedPaths(tc.filePath, old, new)
		if len(warnings) == 0 {
			t.Errorf("%s: expected hardcoded path warning (substring exemption must not apply), got none", tc.name)
		}
	}

	// Real fixture directories stay exempt.
	if warnings := checkHardcodedPaths("internal/mocks/config.go", old, new); len(warnings) != 0 {
		t.Errorf("real mocks segment should stay exempt, got: %v", warnings)
	}
	if warnings := checkHardcodedPaths("test/fixtures/data.go", old, new); len(warnings) != 0 {
		t.Errorf("real fixtures segment should stay exempt, got: %v", warnings)
	}
}

func TestCheckHardcodedPaths_MacOSHomeIntroduced(t *testing.T) {
	old := "package main\n"
	new := `package main

const configPath = "/Users/john/project/config.yaml"
`
	warnings := checkHardcodedPaths("config.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected hardcoded path warning for macOS home path")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "macOS home path") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected macOS home path warning, got: %v", warnings)
	}
}

func TestCheckHardcodedPaths_LinuxHomeIntroduced(t *testing.T) {
	old := "package main\n"
	new := `package main

const binaryPath = "/home/dev/go/bin/custom-go"
`
	warnings := checkHardcodedPaths("main.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected hardcoded path warning for Linux home path")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "Linux home path") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected Linux home path warning, got: %v", warnings)
	}
}

func TestCheckHardcodedPaths_RootHomeIntroduced(t *testing.T) {
	old := "package main\n"
	new := `package main

const certPath = "/root/certs/server.pem"
`
	warnings := checkHardcodedPaths("main.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected hardcoded path warning for Linux root home path")
	}
}

func TestCheckHardcodedPaths_WindowsBackslashIntroduced(t *testing.T) {
	old := "package main\n"
	new := `package main

const toolsPath = "C:\\Users\\admin\\tools\\bin.exe"
`
	warnings := checkHardcodedPaths("config.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected hardcoded path warning for Windows user path")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "Windows user path") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected Windows user path warning, got: %v", warnings)
	}
}

func TestCheckHardcodedPaths_WindowsForwardSlashIntroduced(t *testing.T) {
	old := ""
	new := `const logPath = "D:/Users/admin/logs/app.log"`
	warnings := checkHardcodedPaths("config.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected hardcoded path warning for Windows forward-slash path")
	}
}

func TestCheckHardcodedPaths_PreExistingNotFlagged(t *testing.T) {
	// If the path was already in oldContent, it should NOT be flagged.
	old := `const configPath = "/Users/john/project/config.yaml"`
	new := old + "\n// added comment\n"
	warnings := checkHardcodedPaths("config.go", old, new)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for pre-existing path, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckHardcodedPaths_TestFileExempt(t *testing.T) {
	new := `const path = "/Users/testuser/data"`
	warnings := checkHardcodedPaths("main_test.go", "", new)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for test file, got %d", len(warnings))
	}
}

func TestCheckHardcodedPaths_DocFileExempt(t *testing.T) {
	new := `# Setup

Run the binary at /Users/john/app/binary
`
	warnings := checkHardcodedPaths("README.md", "", new)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for markdown file, got %d", len(warnings))
	}
}

func TestCheckHardcodedPaths_MakefileExempt(t *testing.T) {
	new := `all:
	/Users/john/go/bin/binary build
`
	warnings := checkHardcodedPaths("Makefile", "", new)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for Makefile, got %d", len(warnings))
	}
}

func TestCheckHardcodedPaths_SystemPathNotFlagged(t *testing.T) {
	// System-standard paths like /usr/bin, /tmp, /var/log should NOT be flagged.
	old := "package main\n"
	new := `package main

const binPath = "/usr/bin/python3"
const tmpDir = "/tmp/workdir"
`
	warnings := checkHardcodedPaths("main.go", old, new)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for system paths, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckHardcodedPaths_EnvFileExempt(t *testing.T) {
	new := `CONFIG_PATH=/Users/john/project/config`
	warnings := checkHardcodedPaths(".env", "", new)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for .env file, got %d", len(warnings))
	}
}

func TestCheckHardcodedPaths_FixtureDirExempt(t *testing.T) {
	new := `path := "/Users/john/fixtures/data.json"`
	warnings := checkHardcodedPaths("testdata/config.go", "", new)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for fixture directory, got %d", len(warnings))
	}
}

func TestCheckHardcodedPaths_EmptyContent(t *testing.T) {
	warnings := checkHardcodedPaths("main.go", "", "")
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for empty content, got %d", len(warnings))
	}
}

func TestCheckHardcodedPaths_RelativePathNotFlagged(t *testing.T) {
	old := "package main\n"
	new := `package main

const configPath = "./config/app.yaml"
const dataDir = "../data/files"
`
	warnings := checkHardcodedPaths("main.go", old, new)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for relative paths, got %d: %v", len(warnings), warnings)
	}
}
