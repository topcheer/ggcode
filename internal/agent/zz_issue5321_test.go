package agent

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func issue5321ToolCall(name, args string) provider.ToolCallDelta {
	return provider.ToolCallDelta{Name: name, Arguments: json.RawMessage(args)}
}

// #5321: internal/tool apply_patch.go Execute accepts two argument
// shapes - the schema-advertised top-level {type,path,diff} and a nested
// gateway/replay fallback {"operation":{type,path,diff}} (Execute lifts
// the nested form when the top-level path is empty). Every agent-side
// extractor parsed top-level only, so a nested-form call executed a real
// file edit while all edit-tracking guards silently no-op'd. The shared
// normalizeApplyPatchArgs helper now runs at every extractor entry
// point, mirroring Execute's parsing exactly.

const issue5321Nested = `{"operation":{"type":"update_file","path":"internal/foo.go","diff":"*** Begin Patch\n*** Update File: internal/foo.go\n"}}`
const issue5321NestedCreate = `{"operation":{"type":"create_file","path":"internal/new.go","diff":"*** Begin Patch\n*** Add File: internal/new.go\n"}}`

func TestIssue5321_NormalizeLiftsNestedOperation(t *testing.T) {
	var m map[string]any
	if err := json.Unmarshal([]byte(issue5321Nested), &m); err != nil {
		t.Fatal(err)
	}
	norm := normalizeApplyPatchArgs("apply_patch", m)
	if p, _ := norm["path"].(string); p != "internal/foo.go" {
		t.Fatalf("nested path not lifted, got %q", p)
	}
	if ty, _ := norm["type"].(string); ty != "update_file" {
		t.Fatalf("nested type not lifted, got %q", ty)
	}
	// original map untouched (callers may hold their own view)
	if _, ok := m["path"]; ok {
		t.Fatal("normalize must not mutate the input map")
	}
}

func TestIssue5321_NormalizePassthroughShapes(t *testing.T) {
	// top-level path present -> untouched (same map identity)
	top := map[string]any{"type": "update_file", "path": "a.go", "diff": "x"}
	if got := normalizeApplyPatchArgs("apply_patch", top); got == nil || len(got) != len(top) {
		t.Fatal("top-level form must pass through unchanged")
	}
	// other tools untouched
	other := map[string]any{"operation": map[string]any{"path": "a.go"}}
	if got := normalizeApplyPatchArgs("edit_file", other); !reflect.DeepEqual(got, other) {
		t.Fatal("non-apply_patch tools must pass through unchanged")
	}
	// nested form without usable nested path -> untouched
	empty := map[string]any{"operation": map[string]any{"type": "update_file"}}
	if got := normalizeApplyPatchArgs("apply_patch", empty); !reflect.DeepEqual(got, empty) {
		t.Fatal("nested form without path must pass through unchanged")
	}
	// nil-safe
	if got := normalizeApplyPatchArgs("apply_patch", nil); got != nil {
		t.Fatal("nil map must pass through")
	}
	// raw entry point round-trips passthrough bytes
	raw := json.RawMessage(issue5321Nested)
	if got := normalizeApplyPatchRawArgs("edit_file", raw); string(got) != string(raw) {
		t.Fatal("raw passthrough must be byte-identical for other tools")
	}
}

func TestIssue5321_UnreadEditGuardTrackersSeeNestedPath(t *testing.T) {
	args := json.RawMessage(issue5321Nested)
	if got := extractEditFilePaths("apply_patch", args); len(got) != 1 || got[0] != "internal/foo.go" {
		t.Errorf("guard view: want [internal/foo.go], got %v", got)
	}
	if got := extractTrackedEditPaths("apply_patch", args); len(got) != 1 || got[0] != "internal/foo.go" {
		t.Errorf("tracker view: want [internal/foo.go], got %v", got)
	}
}

func TestIssue5321_CreateFileExtractionNested(t *testing.T) {
	got := extractCreateFilePaths("apply_patch", json.RawMessage(issue5321NestedCreate))
	if len(got) != 1 || got[0] != "internal/new.go" {
		t.Fatalf("nested create_file not registered, got %v", got)
	}
	// update_file nested keeps returning nothing (guard still applies)
	if got := extractCreateFilePaths("apply_patch", json.RawMessage(issue5321Nested)); got != nil {
		t.Fatalf("nested update_file must not register as created, got %v", got)
	}
}

func TestIssue5321_BatchConflictDetectsNestedPath(t *testing.T) {
	tc := issue5321ToolCall("apply_patch", issue5321Nested)
	paths := extractFilePathsForConflict(tc)
	if len(paths) != 1 || paths[0] != "internal/foo.go" {
		t.Fatalf("batch conflict blind to nested form, got %v", paths)
	}
	// same-file conflict must actually fire between nested apply_patch and edit_file
	calls := []provider.ToolCallDelta{
		tc,
		issue5321ToolCall("edit_file", `{"file_path":"internal/foo.go","old_text":"a","new_text":"b"}`),
	}
	warnings := detectBatchEditConflicts(calls)
	if len(warnings) == 0 {
		t.Fatal("nested apply_patch must participate in same-file batch conflicts")
	}
}

func TestIssue5321_FilePathFromArgsSeesNestedPath(t *testing.T) {
	// feeds repetition_tracker, verify_hint postEditVerifyHint, search
	// invalidation, fixAmnesia, undo_blind, speculate via the shared helper
	if got := extractFilePathFromArgs("apply_patch", json.RawMessage(issue5321Nested)); got != "internal/foo.go" {
		t.Fatalf("extractFilePathFromArgs blind to nested form, got %q", got)
	}
}

func TestIssue5321_PropagationRepetitionSeeNestedPath(t *testing.T) {
	s := newEditPropagationState()
	if !s.recordEdit("apply_patch", issue5321Nested) {
		t.Fatal("edit propagation blind to nested form")
	}
	rt := newRepetitionTracker()
	rt.recordEditAttempt("apply_patch", json.RawMessage(issue5321Nested), true)
	if rt.failedEditsByFile["internal/foo.go"] != 1 {
		t.Fatal("repetition tracker blind to nested form")
	}
}

func TestIssue5321_ExtractEditedPathsAndRunStatsSeeNestedPath(t *testing.T) {
	tc := issue5321ToolCall("apply_patch", issue5321Nested)
	if got := extractEditedPaths(tc); len(got) != 1 || got[0] != "internal/foo.go" {
		t.Fatalf("extractEditedPaths blind to nested form, got %v", got)
	}
	var s RunStats
	extractPathsFromToolCall("apply_patch", json.RawMessage(issue5321Nested), &s)
	if len(s.FilesEdited) == 0 {
		t.Fatal("RunStats.FilesEdited blind to nested form")
	}
	s.ToolCalls = map[string]int{"apply_patch": 1}
	if !codeChangedInRun(&s) {
		t.Fatal("todo discipline blind to nested-form edit")
	}
}

func TestIssue5321_TopLevelFormStillWorks(t *testing.T) {
	args := json.RawMessage(`{"type":"update_file","path":"internal/foo.go","diff":"x"}`)
	if got := extractEditFilePaths("apply_patch", args); len(got) != 1 || got[0] != "internal/foo.go" {
		t.Errorf("top-level form regressed: %v", got)
	}
	if got := extractFilePathFromArgs("apply_patch", args); got != "internal/foo.go" {
		t.Errorf("top-level extractFilePathFromArgs regressed: %q", got)
	}
}
