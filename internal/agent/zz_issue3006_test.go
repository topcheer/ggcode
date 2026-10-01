package agent

import "testing"

// zz_issue3006_test.go - regression probes for #3006: apply_patch and
// scaffold_project are disk-mutating registered tools (apply_patch.go
// os.WriteFile; scaffold_project.go tryWriteFile writes a 20+ file skeleton)
// but mutatesSourceTree returned false for both, so all four consumers of the
// predicate (cache invalidation agent.go, redundant-reverify classification,
// parallel scheduling, mutate-outcome inference) silently skipped them.
//
// Fix follows the #1104 ruling: extend mutatesSourceTree, NOT the canonical
// sourceMutatingTools superset (whose 9-tool membership is pinned by #737/
// #153 sync assertions and guarded by TestIssue1104_CanonicalSupersetNotEnlarged).

func TestIssue3006MutatesSourceTreeCoversPatchAndScaffold(t *testing.T) {
	for _, name := range []string{"apply_patch", "scaffold_project"} {
		if !mutatesSourceTree(name) {
			t.Errorf("mutatesSourceTree(%q) = false, want true (#3006: disk-mutating tool must gate caches/reverify/scheduling)", name)
		}
	}
	// The pre-existing sync assertion must still hold (canonical set untouched).
	if !assertEditToolMapsInSync() {
		t.Error("assertEditToolMapsInSync() returned false - canonical set must remain intact per #1104")
	}
}

func TestIssue3006CanonicalSetUnchanged(t *testing.T) {
	// #1104 guard: the fix must not enlarge the pinned canonical superset...
	if sourceMutatingTools["apply_patch"] || sourceMutatingTools["scaffold_project"] {
		t.Error("apply_patch/scaffold_project must stay out of sourceMutatingTools (#1104 ruling: extend mutatesSourceTree instead)")
	}
	// ...and read-only tools must not have been swept into the predicate.
	for _, name := range []string{"read_file", "grep", "run_command", "git_status"} {
		if mutatesSourceTree(name) {
			t.Errorf("mutatesSourceTree(%q) = true, want false (read-only tool)", name)
		}
	}
}
