package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyPreflightStripsEnvAssignments(t *testing.T) {

	if !verifyCommandAvailable(`GOFLAGS="-p=1" make verify-ci`) {
		t.Fatal("env-prefixed make must be detected as available (was: skipped -> false green)")
	}
	if !verifyCommandAvailable("CGO_ENABLED=0 go test ./...") {
		t.Fatal("env-prefixed go must be detected as available")
	}

	if verifyCommandAvailable("zz-definitely-missing-tool-2122 build") {
		t.Fatal("missing binary must remain unavailable")
	}
}

func TestVerifyPreflightCdCompound(t *testing.T) {

	if !verifyCommandAvailable("cd /tmp && true") {
		t.Fatal("cd compound must probe the real segment (cd is a shell builtin with no Linux binary)")
	}
}

func TestExecuteVerifyCommandEnvPrefixedRuns(t *testing.T) {
	if !verifyCommandAvailable(`ZZVAR="1" false`) {
		t.Fatal("env-prefixed false must pass the preflight (it is on PATH) - the old skip was the false green")
	}
}

func TestDetectBuildSystemJustNoRecipeFallsThrough(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "justfile"), []byte("hello:\n    echo hi\n\ndeploy:\n    echo deploy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := detectBuildSystem(dir)
	if strings.HasPrefix(got, "just") {
		t.Fatalf("justfile without verify/ci/test/build recipes must fall through (was: bare %q -> first recipe -> false green)", got)
	}
}

func TestIsRealTestExecutionCleanInstallDenied(t *testing.T) {
	for _, cmd := range []string{"make clean", "make install", "just clean"} {
		if isRealTestExecution(cmd) {
			t.Fatalf("%q must not count as a real test execution (it cleared the failed-build urgency flag)", cmd)
		}
	}
	if !isRealTestExecution("make test") {
		t.Fatal("make test must still count")
	}
}
