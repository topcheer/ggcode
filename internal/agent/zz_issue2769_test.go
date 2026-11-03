package agent

import (
	"encoding/json"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestIssue2769_LongStatedPathMatchesBasename(t *testing.T) {

	stated := normalizePathTarget("/Volumes/new/ggai/ggcode/internal/agent/tool_target_mismatch.go")
	if got := len(stated); got != 63 {
		t.Fatalf("normalizePathTarget should no longer truncate: len=%d, want 63", got)
	}
	actual := normalizePathTarget("tool_target_mismatch.go")
	if !targetsMatch(stated, actual) {
		t.Fatalf("63-char stated path must match same-basename actual (was truncated to mismatch before #2769 fix)")
	}
}

func TestIssue2769_LongPathBothSidesStillMatch(t *testing.T) {

	p := normalizePathTarget("/Volumes/new/ggai/ggcode/internal/agent/tool_target_mismatch.go")
	if !targetsMatch(p, p) {
		t.Fatal("identical long paths must match")
	}
}

func TestIssue2769_LongPathDifferentBasenameStillMismatch(t *testing.T) {

	stated := normalizePathTarget("/Volumes/new/ggai/ggcode/internal/agent/tool_target_mismatch.go")
	actual := normalizePathTarget("/Volumes/new/ggai/ggcode/internal/agent/agent.go")
	if targetsMatch(stated, actual) {
		t.Fatal("different basenames must stay mismatched")
	}
}

func TestIssue2769_TruncationStillAppliedForDisplay(t *testing.T) {

	long := ""
	for i := 0; i < 100; i++ {
		long += "a"
	}
	if got := normalizePathTarget(long); got != long {
		t.Fatalf("comparison normalization must not truncate: got len=%d, want 100", len(got))
	}
}

func TestIssue2769_NoMismatchNoiseForLongAlignedTargets(t *testing.T) {
	stated := extractStatedTargets("let me read /Volumes/new/ggai/ggcode/internal/agent/tool_target_mismatch.go and /Volumes/new/ggai/ggcode/internal/agent/tool_sequence.go")
	calls := []provider.ToolCallDelta{
		{ID: "c1", Name: "read_file", Arguments: json.RawMessage(`{"file_path":"tool_target_mismatch.go"}`)},
		{ID: "c2", Name: "read_file", Arguments: json.RawMessage(`{"file_path":"internal/agent/tool_sequence.go"}`)},
	}
	actual := extractActualToolTargets(calls)
	s := newToolTargetState()
	if msg := s.checkMismatch(stated, actual); msg != "" {
		t.Fatalf("aligned long-path intent must not warn, got: %s", msg)
	}
}
