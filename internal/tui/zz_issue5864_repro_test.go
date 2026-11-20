package tui

import (
	"testing"
	"time"

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
	deadline := time.Now().Add(500 * time.Millisecond) // decision arrives via safego.Go (async)
	for {
		select {
		case d := <-ch:
			if d == permission.Cancelled || d == permission.Deny {
				t.Fatalf("unexpected decision %v", d)
			}
			return
		default:
			if time.Now().After(deadline) {
				t.Fatal("post-fix: Enter must deliver a decision (freeze was: nothing delivered)")
			}
			time.Sleep(5 * time.Millisecond)
		}
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
	deadline := time.Now().Add(500 * time.Millisecond) // decision arrives via safego.Go (async)
	for {
		select {
		case d := <-ch:
			if d == permission.Cancelled || d == permission.Deny {
				t.Fatalf("unexpected decision %v (cursor=0 should be an allow-family option)", d)
			}
			return
		default:
			if time.Now().After(deadline) {
				t.Fatal("clean-composer Enter must deliver a decision")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
}

// Post-fix pin (#5864 layer 2): an approval raised while a teammate
// (impersonate) panel is open must still be answerable — the panel used to
// swallow every key before the approval branch, freezing the TUI.
func TestIssue5864_ApprovalOutranksTeammatePanel(t *testing.T) {
	m, ch := new5864Model()
	m.impersonatePanel = &impersonatePanelState{} // an open modal panel (same swallow-path class as the teammate view)

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)

	if m.pendingApproval != nil {
		t.Fatal("Enter with approval pending + teammate panel open must resolve the approval (was: panel swallowed the key -> freeze)")
	}
	select {
	case d := <-ch:
		if d == permission.Cancelled {
			t.Fatalf("unexpected decision %v", d)
		}
	default:
		t.Fatal("decision must be delivered; pre-fix the gate hung forever")
	}
}
