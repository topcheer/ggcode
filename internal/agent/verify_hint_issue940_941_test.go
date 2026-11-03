package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
		{"assign no-space colon-eq", "test:=foo\nall:\n", "test", false},
		{"assign double-colon-eq", "test::=foo\nall:\n", "test", false},
		{"assign spaced eq", "test = foo\nall:\n", "test", false},
		{"assign upper spaced colon-eq", "TEST := foo\nall:\n", "test", false},
		{"rule with prereq still matches", "test: deps\n\techo\n", "test", true},
		{"double-colon rule still matches", "test::\n\techo\n", "test", true},
	}
	for _, c := range cases {
		if got := hasMakeTarget(c.makefile, c.target); got != c.want {
			t.Errorf("%s: hasMakeTarget(target=%q) = %v, want %v", c.name, c.target, got, c.want)
		}
	}
}

func TestDetectBuildSystemTaskfileAnchoring(t *testing.T) {
	tmp := t.TempDir()

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

	if cmd := detectBuildSystem(hyphenDir); cmd == "task test" || cmd == "task build" || strings.HasPrefix(cmd, "task ") {
		t.Errorf("hyphen-only Taskfile: must not synthesize a task command, got %q (substring match regression #940)", cmd)
	}

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

func TestVerifyCommandAvailableBashScriptPath(t *testing.T) {
	if verifyCommandAvailable("bash /definitely/not/a/real/script_xyz123.sh") {
		t.Error("bash with nonexistent script path should be unavailable")
	}

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
