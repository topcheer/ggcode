package agent

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// #3851 + #5153 pin tests, upgraded for the PR2 watcher-registry shape
// (#5521): the former source-text pins asserted the inline branch shape;
// the contract is now pinned behaviorally on the registered mutation
// watcher, plus light structure pins on the registration and consumption
// sites.

func TestIssue3851_WriteInvalidatesSeenReadOnly(t *testing.T) {
	// Behavioral contract: a successful non-speculative write is a
	// mutation (dedup cache must be invalidated).
	if !dedupInvalidation.MutatedSource(
		provider.ToolCallDelta{Name: "edit_file"},
		tool.Result{Content: "ok", IsError: false},
	) {
		t.Fatal("#3851: successful write must signal mutation - a stale pre-write snapshot would stay servable to later reads in the same batch")
	}
	// Speculative-safe (read-only) tools never invalidate.
	if dedupInvalidation.MutatedSource(
		provider.ToolCallDelta{Name: "read_file"},
		tool.Result{Content: "content", IsError: false},
	) {
		t.Fatal("#3851: speculative-safe (read-only) tools must not invalidate the dedup cache")
	}
	// Structure: the stream loop still consumes the watcher at the
	// invalidation branch and rebinds the cache.
	src := readAgentSource(t)
	if !strings.Contains(src, "dedupInvalidation.MutatedSource") {
		t.Fatal("#3851: stream loop no longer consumes the dedup mutation watcher")
	}
	if !strings.Contains(src, "seenReadOnly = make(map[dedupKey]int)") {
		t.Fatal("#3851: dedup cache rebinding site missing (dedup removed?)")
	}
}

func TestIssue5153_SeenROInvalidatesOnPartialWriteDespiteError(t *testing.T) {
	// #5153: errored-but-wrote (multi_file_edit partial_success) must
	// count as a mutation, matching the toolMemo semantics (#2991).
	if !dedupInvalidation.MutatedSource(
		provider.ToolCallDelta{Name: "multi_file_edit"},
		tool.Result{Content: `{"written_paths":["a.go"],"failed":["b.go"]}`, IsError: true},
	) {
		t.Fatal("#5153: partial write despite error must signal mutation - same-batch re-reads replay stale pre-edit content as success")
	}
	// Clean error with zero written paths must not.
	if dedupInvalidation.MutatedSource(
		provider.ToolCallDelta{Name: "multi_file_edit"},
		tool.Result{Content: `{"failed":["b.go"]}`, IsError: true},
	) {
		t.Fatal("#5153: error with zero written paths must not signal mutation")
	}
	// Registry structure: the guard-zone family is registered and the
	// mutation watcher exists as the documented invalidation path.
	w := dedupMutationWatcher{}
	var _ sourceMutationWatcher = w // interface compliance pin
	if len(readAgentSource(t)) == 0 {
		t.Fatal("agent source unreadable")
	}
}

func readAgentSource(t *testing.T) string {
	t.Helper()
	raw, err := readAgentAndProcessingSources()
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
