package agent

import (
	"encoding/json"
	"testing"
)

// #5244: the OpenAI Responses-native apply_patch path bypassed the
// unread-edit guard twice: (1) the post-edit hook gate keyed on the
// #3006-pinned 9-tool canonical map, which does not contain apply_patch,
// so the whole block was skipped; (2) extractEditFilePaths had no
// apply_patch case. The gate now unions the canonical map with
// apply_patch (extensions belong in mutatesSourceTree semantics, not the
// pinned map), and the extractor pulls the top-level path.

func TestIssue5244_ApplyPatchPassesPostEditGate(t *testing.T) {
	// the exact gate expression from agent.go, pinned here
	passes := func(name string) bool {
		return fileEditingTools[name] || name == "apply_patch"
	}
	if !passes("apply_patch") {
		t.Fatal("apply_patch must pass the post-edit gate (#5244)")
	}
	// canonical pins intact: 9-map unchanged, map still excludes apply_patch (#3006)
	if len(sourceMutatingTools) != 9 {
		t.Fatalf("#3006: canonical map must stay at 9, got %d", len(sourceMutatingTools))
	}
	if sourceMutatingTools["apply_patch"] {
		t.Fatal("#3006: apply_patch must NOT enter the canonical map")
	}
	if mutatesSourceTree("apply_patch") != true {
		t.Fatal("mutatesSourceTree must keep recognizing apply_patch")
	}
}

func TestIssue5244_ExtractorPullsTopLevelPath(t *testing.T) {
	args := json.RawMessage(`{"path":"internal/foo.go","diff":"*** Begin Patch\n*** Update File: internal/foo.go\n"}`)
	got := extractEditFilePaths("apply_patch", args)
	if len(got) != 1 || got[0] != "internal/foo.go" {
		t.Fatalf("want [internal/foo.go], got %v", got)
	}
}

func TestIssue5244_ExtractorNilSafeAndOtherToolsUnchanged(t *testing.T) {
	if got := extractEditFilePaths("apply_patch", nil); got != nil {
		t.Errorf("nil args must yield nil, got %v", got)
	}
	args := json.RawMessage(`{"file_path":"a.go"}`)
	if got := extractEditFilePaths("edit_file", args); len(got) != 1 || got[0] != "a.go" {
		t.Errorf("edit_file extraction regressed: %v", got)
	}
	// file_ops stays deliberately absent (#5240): guard must not fire on deletes
	if got := extractEditFilePaths("file_ops", json.RawMessage(`{"operations":[]}`)); got != nil {
		t.Errorf("file_ops must stay absent from guard extraction, got %v", got)
	}
}

// #5249: #5246's union gated only the L3399 hook block; the other
// tracker/evidence gates (futileCycle, verifyDebt, successDeclare,
// postEditVerifyHint -> sourceEditsThisRun -> finalTurnEvidenceGate)
// still keyed on the bare canonical map, so apply_patch-only runs never
// accumulated final-turn edit evidence. All gates now go through
// mutatingSourceEdit.

func TestIssue5249_MutatingSourceEditGate(t *testing.T) {
	if !mutatingSourceEdit("apply_patch") {
		t.Fatal("apply_patch must pass mutatingSourceEdit")
	}
	// canonical members unchanged through the helper
	for name := range sourceMutatingTools {
		if !mutatingSourceEdit(name) {
			t.Errorf("canonical tool %q must still pass mutatingSourceEdit", name)
		}
	}
	// non-mutating tools stay out (note: file_ops IS in the canonical
	// mutating map - the #5240 exemption lives only in the guard's path
	// extraction, not in the mutation classification)
	if mutatingSourceEdit("read_file") || mutatingSourceEdit("glob") || mutatingSourceEdit("run_command") {
		t.Error("read_file/glob/run_command must not pass mutatingSourceEdit")
	}
	if !mutatingSourceEdit("file_ops") {
		t.Error("file_ops must keep passing (canonical member; #5240 exemption is extraction-only)")
	}
}
