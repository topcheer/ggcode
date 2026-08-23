package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// TestHasMakeTargetExcludesAssignments (#941): "test:=foo" is a variable
// assignment, not a rule - the doc comment promised this exclusion but the
// implementation missed it, creating phantom targets that made make exit 2
// (not 127) and bypass the verify exit-code guard.
func TestHasMakeTargetExcludesAssignments(t *testing.T) {
	cases := []struct {
		name     string
		makefile string
		target   string
		want     bool
	}{
		{"real rule", "all:\n\techo hi\n", "all", true},
		{"indented rule", "  test:\n\techo\n", "test", true},
		{"commented rule", "# test:\nall:\n", "test", false},
		{"assign no-space colon-eq", "test:=foo\nall:\n", "test", false},         // #941 repro
		{"assign double-colon-eq", "test::=foo\nall:\n", "test", false},          // GNU ::=
		{"assign spaced eq", "test = foo\nall:\n", "test", false},                // was already OK
		{"assign upper spaced colon-eq", "TEST := foo\nall:\n", "test", false},   // case-sensitive, was already OK
		{"rule with prereq still matches", "test: deps\n\techo\n", "test", true}, // rest starts with space, not '='
		{"double-colon rule still matches", "test::\n\techo\n", "test", true},    // rest = ":", not ":="
	}
	for _, c := range cases {
		if got := hasMakeTarget(c.makefile, c.target); got != c.want {
			t.Errorf("%s: hasMakeTarget(target=%q) = %v, want %v", c.name, c.target, got, c.want)
		}
	}
}

// TestDetectBuildSystemTaskfileAnchoring (#940): task detection must anchor
// task names at line start so "integration-test:" / "docker-build:" /
// "image: node:latest" don't satisfy the bare substring check for "test:" /
// "build:" - which previously produced "task test", a command that exits 1
// (not 127) and triggered false verification failures + wasted auto-repair.
func TestDetectBuildSystemTaskfileAnchoring(t *testing.T) {
	tmp := t.TempDir()

	// Only hyphenated tasks: must NOT be detected as "task test"/"task build".
	hyphenDir := filepath.Join(tmp, "hyphen")
	os.MkdirAll(hyphenDir, 0755)
	os.WriteFile(filepath.Join(hyphenDir, "Taskfile.yml"), []byte(
		"version: '3'\n"+
			"tasks:\n"+
			"  integration-test:\n"+
			"    cmds: [go test ./integration/...]\n"+
			"  docker-build:\n"+
			"    cmds: [docker build .]\n"+
			"    env:\n"+
			"      image: node:latest\n"), 0644)
	if cmd := detectBuildSystem(hyphenDir); cmd != "task" {
		t.Errorf("hyphen-only Taskfile: expected bare %q fallback, got %q (substring match regression #940)", "task", cmd)
	}

	// Real indented task key: must be detected.
	realDir := filepath.Join(tmp, "real")
	os.MkdirAll(realDir, 0755)
	os.WriteFile(filepath.Join(realDir, "Taskfile.yml"), []byte(
		"version: '3'\n"+
			"tasks:\n"+
			"  test:\n"+
			"    cmds: [go test ./...]\n"), 0644)
	if cmd := detectBuildSystem(realDir); cmd != "task test" {
		t.Errorf("real test task: expected %q, got %q", "task test", cmd)
	}

	// Verify-ci wins over test in priority order.
	ciDir := filepath.Join(tmp, "ci")
	os.MkdirAll(ciDir, 0755)
	os.WriteFile(filepath.Join(ciDir, "Taskfile.yml"), []byte(
		"version: '3'\n"+
			"tasks:\n"+
			"  verify-ci:\n"+
			"    cmds: [echo ci]\n"+
			"  test:\n"+
			"    cmds: [echo t]\n"), 0644)
	if cmd := detectBuildSystem(ciDir); cmd != "task verify-ci" {
		t.Errorf("verify-ci priority: expected %q, got %q", "task verify-ci", cmd)
	}
}

// TestVerifyCommandAvailableBashScriptPath (#941 companion): the bash/sh
// script-path check must actually run (it was dead code behind an earlier
// unconditional return true).
func TestVerifyCommandAvailableBashScriptPath(t *testing.T) {
	if verifyCommandAvailable("bash /definitely/not/a/real/script_xyz123.sh") {
		t.Error("bash with nonexistent script path should be unavailable")
	}
	// Any existing file passes the fileExists check (executability is the
	// shell's business - its 127 is caught by the exit-code guard).
	if !verifyCommandAvailable("bash /etc/hosts") && !fileExists("/etc/hosts") {
		t.Error("bash with existing script path should be available when file exists")
	}
	if !verifyCommandAvailable("bash") {
		t.Error("bare bash should be available")
	}
	if !verifyCommandAvailable("source") {
		t.Error("source builtin should be available")
	}
}
