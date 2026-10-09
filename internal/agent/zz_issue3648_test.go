package agent

// #3648 probes: (1) root-package edits (main.go at repo root) must produce
// a scoped `go test .` instead of vanishing the whole Go TIA chain; (2) the
// goPkgArg rendering must keep normal dirs intact.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupRootPkgRepo creates a temp git repo whose only Go files live at the
// module root, commits them, then modifies main.go so it shows up in
// git-changed files.
func setupRootPkgRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	write := func(name, content string) {
		t.Helper()
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module test/root\n\ngo 1.27\n")
	write("main.go", "package main\n\nfunc main() {}\n")
	write("main_test.go", "package main\n\nimport \"testing\"\n\nfunc TestMainFn(t *testing.T) {}\n")
	run("add", ".")
	run("-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "init")
	// Modify the root package AFTER commit so changedGoFilesFromGit sees it.
	write("main.go", "package main\n\nfunc main() { println(1) }\n")
	return dir
}

func TestIssue3648_RootPackageEditGetsScopedTestCommand(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := setupRootPkgRepo(t)

	cmd := impactScopedTestCommand(dir)
	if cmd == "" {
		t.Fatal("root-package edit must yield a scoped test command, got empty string (whole TIA chain dead)")
	}
	if !strings.HasPrefix(cmd, "go test") || !strings.Contains(cmd, " .") {
		t.Fatalf("root package must be tested as '.', got: %q", cmd)
	}
	if strings.Contains(cmd, "././") {
		t.Fatalf("invalid package arg rendered: %q", cmd)
	}
}

func TestIssue3648_GoPkgArgRendering(t *testing.T) {
	if goPkgArg(".") != "." {
		t.Fatalf("root package must render as '.', got %q", goPkgArg("."))
	}
	if goPkgArg("internal/util") != "./internal/util/" {
		t.Fatalf("normal dir rendering broken: %q", goPkgArg("internal/util"))
	}
}
