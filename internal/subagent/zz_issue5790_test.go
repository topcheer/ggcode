package subagent

// #5790: rereadBreakerEditTools missed "apply_patch" (the OpenAI
// Responses-native patch executor, internal/tool/apply_patch.go): a
// Responses-native sub-agent doing read -> apply_patch edit ->
// re-read verification never had its reread window reset, so the
// strict #5013 breaker killed the run on the 4th read of the same
// file - the exact "re-reading a file you just changed is legitimate
// verification" shape the breaker's own header comment promises to
// protect. internal/agent counts apply_patch as a source-mutating
// edit everywhere (#5244/#5249/#5254/#5321 via mutatingSourceEdit);
// the breaker map must too.

import (
	"encoding/json"
	"testing"
)

func TestIssue5790ApplyPatchResetsRereadWindow(t *testing.T) {
	b := newRereadBreakerWithPolicy(RereadPolicyDefault)
	read := mustArgs5790(t, map[string]any{"path": "main.go"})
	patch := mustArgs5790(t, map[string]any{
		"type": "update_file",
		"path": "main.go",
		"diff": "*** Begin Patch\n*** Update File: main.go\n@@\n-old\n+new\n*** End Patch",
	})

	// The #5790 shape: read, edit (apply_patch), verify-read, edit,
	// verify-read. Each apply_patch mutation must reset the same-path read
	// window like edit_file does, so legitimate verification reads never
	// trip the strict breaker.
	for round := 0; round < 3; round++ {
		for i := 1; i <= 3; i++ {
			if _, _, action := b.recordAction("read_file", read); action != rereadNone {
				t.Fatalf("round %d read #%d: action=%v, want none", round, i, action)
			}
		}
		if _, _, action := b.recordAction("apply_patch", patch); action != rereadNone {
			t.Fatalf("round %d apply_patch: action=%v, want none", round, action)
		}
	}
	if b.tripped != "" {
		t.Fatalf("breaker tripped on %q: apply_patch edits must reset the reread window (#5790)", b.tripped)
	}
}

func TestIssue5790ApplyPatchMakesToolsetEditable(t *testing.T) {
	// runIsReadOnly drives RereadPolicyAuto: a toolset whose only mutation
	// is apply_patch is NOT read-only - Auto must resolve it to the strict
	// breaker, not the research calibration.
	if runIsReadOnly([]string{"apply_patch"}, nil) {
		t.Fatal("allowedTools=[apply_patch] must count as an editable run: apply_patch mutates source (#5790)")
	}
}

func mustArgs5790(t *testing.T, m map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return string(raw)
}
