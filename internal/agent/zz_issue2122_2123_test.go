package agent

// #2122/#2123 regression:
//   - #2122: the verify preflight took Fields(command)[0] - a leading env
//     assignment (GOFLAGS="..." make verify-ci, this repo's canonical
//     form) or a cd compound failed LookPath and the whole verification
//     was silently skipped while reporting Passed=true (e2e probe: a
//     literal `false` payload went green).
//   - #2123 P1: the just/task branches returned the BARE command when no
//     verify/ci/test/build recipe matched - bare `just` runs the first
//     recipe (usually exit 0, unrelated output): a guaranteed false
//     green. P2: make clean/install cleared the lastBuildFailed urgency
//     flag (#1841 residue).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyPreflightStripsEnvAssignments(t *testing.T) {
	// make IS available in CI/dev environments; the point is the env
	// prefix no longer decides availability.
	if !verifyCommandAvailable(`GOFLAGS="-p=1" make verify-ci`) {
		t.Fatal("env-prefixed make must be detected as available (was: skipped -> false green)")
	}
	if !verifyCommandAvailable("CGO_ENABLED=0 go test ./...") {
		t.Fatal("env-prefixed go must be detected as available")
	}
	// A genuinely missing tool still reports unavailable (no blanket pass).
	if verifyCommandAvailable("zz-definitely-missing-tool-2122 build") {
		t.Fatal("missing binary must remain unavailable")
	}
}

func TestVerifyPreflightCdCompound(t *testing.T) {
	// `true` is POSIX-guaranteed on every runner (go itself may be off
	// the test process's PATH in CI).
	if !verifyCommandAvailable("cd /tmp && true") {
		t.Fatal("cd compound must probe the real segment (cd is a shell builtin with no Linux binary)")
	}
}

// The e2e shape from the issue: `false` IS on PATH, so an env-prefixed
// doomed payload now passes the preflight, RUNS, and fails - the old
// preflight skipped it as "tool not available" and reported green.
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
