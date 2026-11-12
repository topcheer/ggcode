package agent

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// #5249 residuals (not covered by the #5250 five-gate replay, verified via
// pr-diff grep): (1) agent.go's error-path gate `result.IsError &&
// fileEditingTools[...]` never fed editFailRecovery for native apply_patch
// failures; (2) extractCreateFilePaths had no apply_patch case, so
// apply_patch create_file on a brand-new file fired the unread-edit nag
// while write_file on the same new file was exempt (recordCreated parity).
func TestIssue5249CreateFileExemptFromUnreadNag(t *testing.T) {
	created := extractCreateFilePaths("apply_patch", json.RawMessage(`{"type":"create_file","path":"internal/new.go","diff":"..."}`))
	if len(created) != 1 || created[0] != "internal/new.go" {
		t.Fatalf("apply_patch create_file must register as created: got %v", created)
	}
	// update_file / delete_file variants must NOT register (they target
	// existing content; the unread-edit guard still applies to them).
	for _, variant := range []string{"update_file", "delete_file"} {
		if got := extractCreateFilePaths("apply_patch", json.RawMessage(`{"type":"`+variant+`","path":"internal/old.go"}`)); got != nil {
			t.Errorf("apply_patch %s must not register as created: got %v", variant, got)
		}
	}
	// write_file parity anchor.
	if got := extractCreateFilePaths("write_file", json.RawMessage(`{"path":"w.go"}`)); len(got) != 1 || got[0] != "w.go" {
		t.Fatalf("write_file parity broken: got %v", got)
	}
}

// TestIssue5249ErrorPathGateUsesPredicate pins the agent.go error-path gate:
// native apply_patch failures must reach editFailRecovery via the same
// mutatingSourceEdit predicate the success-path gates use (#5250), not the
// bare canonical map. Source-level pin (grep precedent: no-bare-gofmt test).
func TestIssue5249ErrorPathGateUsesPredicate(t *testing.T) {
	root, err := os.Executable()
	if err != nil {
		t.Skip("no executable path")
	}
	_ = root
	// #5543: the error-path gate moved out of agent.go; scan the
	// stream-loop source files (shape-agnostic file targeting).
	src, err := readStreamLoopSource("agent.go", "run_tool_call_idx.go")
	if err != nil {
		t.Fatalf("stream-loop source unreadable: %v", err)
	}
	if !strings.Contains(src, "result.IsError && mutatingSourceEdit(") {
		t.Error("agent.go error-path gate must use mutatingSourceEdit (apply_patch failures feed editFailRecovery)")
	}
	if strings.Contains(src, "result.IsError && fileEditingTools[") {
		t.Error("agent.go still contains a bare error-path fileEditingTools gate")
	}
}
