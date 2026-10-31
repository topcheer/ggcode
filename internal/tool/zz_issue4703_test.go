//go:build linux

package tool

import (
	"strings"
	"testing"
)

// #4703: the Linux ghostty new_tab/new_window paths must honor the same
// cd-only contract darwin has had since #2589/#1324/#3140:
//   - command empty + working_dir set -> the surface still starts in wd
//     (cd-only sh -c argv; the old bare new-window call silently dropped wd);
//   - working_dir empty -> fall back to the tool process cwd (schema:
//     "defaults to cwd"), never a Ghostty-chosen default.
// gdbusCall shells out to the real bus, so the companion tests pin the pure
// argv/param builders and the directory resolution; the execute paths are
// thin compositions of exactly these helpers.

func TestIssue4703CdOnlyArgvCarriesWorkingDir(t *testing.T) {
	argv := ghosttyLinuxCdOnlyArgv("/tmp/with space")
	if len(argv) != 3 || argv[0] != "sh" || argv[1] != "-c" {
		t.Fatalf("cd-only argv = %v, want [sh -c \"cd ...\"]", argv)
	}
	// The cd target must be single-quoted so spaces survive sh -c.
	if !strings.HasPrefix(argv[2], "cd ") || !strings.Contains(argv[2], "'/tmp/with space'") {
		t.Fatalf("cd-only shell cmd = %q, want cd '<wd>' with shell quoting", argv[2])
	}
}

func TestIssue4703CommandArgvStillCdsWithCommand(t *testing.T) {
	argv := ghosttyLinuxCommandArgv("make test", "/proj")
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "cd '/proj'") || !strings.Contains(joined, "make test") {
		t.Fatalf("command argv = %v, want cd-then-command", argv)
	}
}

func TestIssue4703CdOnlyParamsShape(t *testing.T) {
	params := ghosttyLinuxNewWindowCommandParams(ghosttyLinuxCdOnlyArgv("/w"))
	if !strings.HasPrefix(params, "[<[\"-e\"") || !strings.HasSuffix(params, ")]>") {
		t.Fatalf("gdbus params = %q, want new-window-command array form", params)
	}
	if !strings.Contains(params, `"sh"`) || !strings.Contains(params, `"cd '/w'"`) {
		t.Fatalf("gdbus params = %q, want the cd-only argv embedded", params)
	}
}

func TestIssue4703ResolveWorkingDirFallsBackToProcessCwd(t *testing.T) {
	g := &GhosttyTool{}
	got := g.resolveWorkingDir("   ")
	if got == "" {
		t.Fatal("empty working_dir must fall back to g.workingDir() (schema: defaults to cwd), got empty")
	}
	explicit := g.resolveWorkingDir("/explicit")
	if explicit != "/explicit" {
		t.Fatalf("explicit working_dir = %q, want it preserved", explicit)
	}
}
