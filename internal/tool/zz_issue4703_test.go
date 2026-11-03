//go:build linux

package tool

import (
	"strings"
	"testing"
)

func TestIssue4703CdOnlyArgvCarriesWorkingDir(t *testing.T) {
	argv := ghosttyLinuxCdOnlyArgv("/tmp/with space")
	if len(argv) != 3 || argv[0] != "sh" || argv[1] != "-c" {
		t.Fatalf("cd-only argv = %v, want [sh -c \"cd ...\"]", argv)
	}

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
	if !strings.HasPrefix(params, "[<[\"-e\"") || !strings.HasSuffix(params, "]>]") {
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
