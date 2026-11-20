package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/permission"
)

// new5864Model builds an approval-pending model like zz_issue5843_test.go does.
func new5864Model() (Model, chan permission.Decision) {
	m := newTestModel()
	ch := make(chan permission.Decision, 1)
	m.pendingApproval = &ApprovalMsg{
		ToolName: "run_command",
		Input:    `{"command":"cat >> ~/.ggcode/keys.env <<'EOF'\nKEY=value\nEOF"}`,
		Response: ch,
	}
	m.approvalOptions = defaultApprovalOptions()
	m.approvalCursor = 0 // whatever; Enter must resolve regardless when composer clean
	return m, ch
}

// Post-fix pin (#5864): invisible residue (zero-width space) must NOT count
// as composerBusy — Enter resolves the approval instead of being swallowed.
// Pre-fix this froze: TrimSpace kept busy=true, Enter queued forever, gate
// never resolved. See issue comment 7035103601 for the original repro shape.
func TestIssue5864_InvisibleResidueMakesComposerBusy(t *testing.T) {
	m, ch := new5864Model()
	m.input.SetValue("\u200b") // zero-width space: visually empty

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)

	if m.pendingApproval != nil {
		t.Fatal("post-fix: Enter must resolve the approval even with invisible residue — prompt stayed (freeze)")
	}
	select {
	case d := <-ch:
		if d == permission.Cancelled || d == permission.Deny {
			t.Fatalf("unexpected decision %v", d)
		}
	default:
		t.Fatal("post-fix: Enter must deliver a decision (freeze was: nothing delivered)")
	}
}

// Fact check 2: clean composer + Enter on cursor resolves the gate (control).
func TestIssue5864_CleanComposerEnterResolves(t *testing.T) {
	m, ch := new5864Model()

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)

	if m.pendingApproval != nil {
		t.Fatal("clean-composer Enter must consume the approval prompt")
	}
	select {
	case d := <-ch:
		if d != permission.Allow {
			t.Fatalf("cursor=0 is Allow(y)? got %v (option order may differ — see defaultApprovalOptions)", d)
		}
	default:
		t.Fatal("clean-composer Enter must deliver a decision")
	}
}
