package agent

import "testing"

// zz_issue3005_3006_test.go - regression probes.
//
// #3005: isRealTestExecution must classify per compound-command segment
// (like isVerifyCommand/isStrictVerifyCommand, #1462-C) and must not treat
// no-op make targets as verification. The old whole-string prefix match
// split the verify-counter reset from the lastBuildFailed update for
// `cd pkg && go test ./`, and `make fmt` reset the counter faking
// "verified" state.
//
// #3006: mutatesSourceTree must cover apply_patch and scaffold_project
// (both persist edits to disk) so change tracking / cache invalidation /
// reverify decisions fire for them.

func TestIssue3005_CompoundTestCommandIsReal(t *testing.T) {
	if !isRealTestExecution("cd internal/agent && go test ./") {
		t.Fatal("#3005: `cd pkg && go test ./` must count as real test execution")
	}
	if !isRealTestExecution("go build ./... ; go test ./internal/agent/") {
		t.Fatal("#3005: `;`-compound with go test segment must count")
	}
	if isRealTestExecution("cd pkg && ls -la") {
		t.Fatal("#3005: compound without any test segment must not count")
	}
}

func TestIssue3005_MakeNoopTargetsDoNotCount(t *testing.T) {
	for _, cmd := range []string{
		"make clean", "make fmt", "make format", "make lint", "make tidy",
		"make deploy", "make build", "make check", "make info",
		"make help", "make list", "make install", "make -C dir target",
	} {
		if isRealTestExecution(cmd) {
			t.Fatalf("#3005: %q must not count as real test execution (no-op target reset the verify counter)", cmd)
		}
	}
}

func TestIssue3005_MakeTestishTargetsStillCount(t *testing.T) {
	for _, cmd := range []string{
		"make test", "make ci", "make e2e", "make verify", "just test",
		"task test", "make verify-ci",
	} {
		if !isRealTestExecution(cmd) {
			t.Fatalf("#3005: %q (verification-ish target) must still count", cmd)
		}
	}
}

func TestIssue3005_BareTestCommandsUnchanged(t *testing.T) {
	for _, cmd := range []string{
		"go test ./...", "pytest", "npm test", "cargo test", "make test",
	} {
		if !isRealTestExecution(cmd) {
			t.Fatalf("#3005 regression: bare %q must stay classified", cmd)
		}
	}
	if isRealTestExecution("go test --help") {
		t.Fatal("#3005: --help must stay excluded")
	}
}

func TestIssue3006_MutatesSourceTreeCoversNewTools(t *testing.T) {
	if !mutatesSourceTree("apply_patch") {
		t.Fatal("#3006: apply_patch persists edits and must mutate")
	}
	if !mutatesSourceTree("scaffold_project") {
		t.Fatal("#3006: scaffold_project writes 20+ files and must mutate")
	}
	// Existing members unchanged.
	if !mutatesSourceTree("edit_file") || !mutatesSourceTree("undo_edit") || !mutatesSourceTree("file_ops") {
		t.Fatal("#3006 regression: existing mutating tools must stay covered")
	}
	// Read-only tools stay clean.
	if mutatesSourceTree("read_file") || mutatesSourceTree("grep") || mutatesSourceTree("search_files") {
		t.Fatal("#3006 regression: read-only tools must not mutate")
	}
}

func TestIssue3006_CanonicalMapStillPinnedAtNine(t *testing.T) {
	// #737/#1104 pin the canonical sourceMutatingTools at 9 members; the
	// #3006 extension deliberately lives in mutatesSourceTree, not the map.
	if len(sourceMutatingTools) != 9 {
		t.Fatalf("#3006: sourceMutatingTools must stay at 9 (got %d); extensions belong in mutatesSourceTree", len(sourceMutatingTools))
	}
	if sourceMutatingTools["apply_patch"] || sourceMutatingTools["scaffold_project"] {
		t.Fatal("#3006: new tools must not enter the #737-pinned canonical map")
	}
}

// TestIssue3011_VerifyCommandSegmentExcludesNoopTargets: the counter-reset
// side must share the makeRunnerNoopTargets exclusion (#3011 - #3005 fixed
// only the lastBuildFailed side; `make clean` still reset the counter).
func TestIssue3011_VerifyCommandSegmentExcludesNoopTargets(t *testing.T) {
	for _, cmd := range []string{
		"make clean", "make fmt", "make lint", "make tidy", "make deploy",
		"make build", "make check", "make help", "just fmt", "task clean",
	} {
		if isVerifyCommand(cmd) {
			t.Fatalf("#3011: %q must not count as a verify command (counter reset)", cmd)
		}
	}
	for _, cmd := range []string{"make test", "make ci", "make verify-ci", "just test", "make"} {
		if !isVerifyCommand(cmd) {
			t.Fatalf("#3011 regression: %q must stay a verify command", cmd)
		}
	}
}
