package tui

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/session"
)

func newSes688() *session.Session {
	return session.NewSession("", "", "")
}

func TestIssue688_ClearNewSessionSyncsPolicyMode(t *testing.T) {
	m := newTestModel()
	cp := permission.NewConfigPolicyWithMode(nil, nil, permission.AutopilotMode)
	m.policy = cp

	m.mode = permission.AutopilotMode
	if got := cp.Mode(); got != permission.AutopilotMode {
		t.Fatalf("setup: policy mode = %v, want autopilot", got)
	}

	m.config = &config.Config{DefaultMode: "supervised"}

	ses := newSes688()
	m.switchToSession(ses, true)

	want := permission.ParsePermissionMode(m.config.DefaultMode)
	if m.mode != want {
		t.Errorf("displayed mode = %v, want %v", m.mode, want)
	}
	if got := cp.Mode(); got != want {
		t.Errorf("policy mode = %v, want %v (policy not synced to global default)", got, want)
	}
}

func TestIssue688_ResumeSessionSyncsPolicyMode(t *testing.T) {
	m := newTestModel()
	cp := permission.NewConfigPolicy(nil, nil)
	m.policy = cp
	m.mode = cp.Mode()

	ses := newSes688()
	ses.PermissionMode = "autopilot"
	m.switchToSession(ses, false)

	if m.mode != permission.BypassMode {
		t.Errorf("displayed mode = %v, want bypass (autopilot normalizes)", m.mode)
	}
	if got := cp.Mode(); got != permission.BypassMode {
		t.Errorf("policy mode = %v, want bypass (autopilot normalizes)", got)
	}
}

func TestIssue688_SwitchSessionClearsPendingApprovals(t *testing.T) {
	m := newTestModel()
	if cp, ok := m.policy.(*permission.ConfigPolicy); ok {
		m.mode = permission.SupervisedMode
		cp.SetMode(permission.SupervisedMode)
	}

	approvalCh := make(chan permission.Decision, 1)
	m.pendingApproval = &ApprovalMsg{
		ToolName: "run_command",
		Input:    `{"command":"ls"}`,
		Response: approvalCh,
	}
	diffCh := make(chan bool, 1)
	m.pendingDiffConfirm = &DiffConfirmMsg{Response: diffCh}
	m.tunnelPendingApprovalID = "old-approval"

	m.switchToSession(newSes688(), true)

	if m.pendingApproval != nil {
		t.Error("pendingApproval not cleared on session switch")
	}
	if m.pendingDiffConfirm != nil {
		t.Error("pendingDiffConfirm not cleared on session switch")
	}
	if m.tunnelPendingApprovalID != "" {
		t.Errorf("tunnelPendingApprovalID = %q, want empty", m.tunnelPendingApprovalID)
	}
	if m.pendingQuestionnaire != nil {
		t.Error("pendingQuestionnaire not cleared on session switch")
	}
	select {
	case d := <-approvalCh:
		if d != permission.Deny {
			t.Errorf("blocked approval released with %v, want Deny", d)
		}
	default:
		t.Error("blocked approval waiter was not released")
	}
	select {
	case ok := <-diffCh:
		if ok {
			t.Error("blocked diff confirm released with true, want false")
		}
	default:
		t.Error("blocked diff confirm waiter was not released")
	}
}

func TestIssue688_ModeSwitchClearsPendingApprovals(t *testing.T) {
	m := newTestModel()
	m.pendingApproval = &ApprovalMsg{
		ToolName: "run_command",
		Input:    `{"command":"ls"}`,
		Response: make(chan permission.Decision, 1),
	}
	m.tunnelPendingApprovalID = "stale"

	updated, _ := m.handleModeSwitch()
	m2 := updated.(Model)
	if m2.pendingApproval != nil {
		t.Error("pendingApproval not cleared on Shift+Tab mode switch")
	}
	if m2.tunnelPendingApprovalID != "" {
		t.Errorf("tunnelPendingApprovalID = %q, want empty after mode switch", m2.tunnelPendingApprovalID)
	}

	m3 := newTestModel()
	m3.pendingApproval = &ApprovalMsg{
		ToolName: "run_command",
		Input:    `{"command":"ls"}`,
		Response: make(chan permission.Decision, 1),
	}
	m3.handleModeCommand([]string{"mode", "bypass"})
	if m3.pendingApproval != nil {
		t.Error("pendingApproval not cleared on /mode command")
	}
	if m3.mode != permission.BypassMode {
		t.Errorf("mode = %v, want bypass", m3.mode)
	}
}
