package agent

import (
	"os"
	"strings"
	"testing"
)

// #5153: the seenReadOnly invalidation branch skipped "errored but wrote"
// results (multi_file_edit partial_success), so a same-batch re-read replayed
// stale pre-edit content with IsError false. The invalidation condition must
// match the toolMemo site (#2991): !IsError || wroteDespiteError.

func TestIssue5153_SeenROInvalidatesOnPartialWriteDespiteError(t *testing.T) {
	raw, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)

	regAt := strings.Index(src, "seenReadOnly[dedupK] = len(toolResults) - 1")
	invAt := strings.Index(src, "seenReadOnly = make(map[dedupKey]int)")
	if regAt < 0 || invAt < 0 || invAt < regAt {
		t.Fatal("dedup registration/invalidation sites missing or reordered")
	}
	branch := src[regAt:invAt]

	// The invalidation else-if must treat wrote-despite-error as a mutation:
	// helper must appear in the condition, not only at the toolMemo site.
	if !strings.Contains(branch, "partialEditWroteDespiteError(") {
		t.Fatal("#5153: seenReadOnly invalidation condition ignores partial writes despite error - same-batch re-reads replay stale pre-edit content as success")
	}
	// Success path must remain (existing #3851 contract).
	if !strings.Contains(branch, "!result.IsError") {
		t.Fatal("#3851 contract broken: invalidation must fire on successful non-speculative writes")
	}
	// The wrote-despite-error call must be inside the invalidation condition,
	// wired to the current tool call's name/content/error.
	if !strings.Contains(branch, "partialEditWroteDespiteError(tc.Name, result.Content, result.IsError)") {
		t.Fatal("#5153: wroteDespiteError check must use the current tool call's name, content and error flag")
	}
}

func TestIssue5153_HelperMatchesMemoSemantics(t *testing.T) {
	// partialEditWroteDespiteError: multi_file_edit with IsError + written
	// paths in content must count as a mutation; clean errors must not.
	content := `{"written_paths":["a.go"],"failed":["b.go"]}`
	if !partialEditWroteDespiteError("multi_file_edit", content, true) {
		t.Fatal("partial write despite error must be detected as a mutation")
	}
	if partialEditWroteDespiteError("multi_file_edit", content, false) {
		t.Fatal("non-error result must not take the wroteDespiteError path")
	}
	if partialEditWroteDespiteError("multi_file_edit", `{"failed":["b.go"]}`, true) {
		t.Fatal("error with zero written paths must not count as a mutation")
	}
	if partialEditWroteDespiteError("read_file", content, true) {
		t.Fatal("non-edit tools must not take the wroteDespiteError path")
	}
}
