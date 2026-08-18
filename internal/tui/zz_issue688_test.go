package tui

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/session"
)

// newSes688 creates a fresh session for switchToSession tests.
func newSes688() *session.Session {
	return session.NewSession("", "", "")
}

// Issue #688 HIGH: /clear (switchToSession isNew=true) must sync the
// ConfigPolicy's internal mode to the global default, not just the displayed
// m.mode. Otherwise a session A autopilot/bypass mode silently survives into
// the new session and auto-allows dangerous tools while the status bar shows
// the global default.
func TestIssue688_ClearNewSessionSyncsPolicyMode(t *testing.T) {
	m := newTestModel()
	cp := permission.NewConfigPolicyWithMode(nil, nil, permission.AutopilotMode)
	m.policy = cp
	// Simulate session A being in autopilot (both UI and policy).
	m.mode = permission.AutopilotMode
	if got := cp.Mode(); got != permission.AutopilotMode {
		t.Fatalf("setup: policy mode = %v, want autopilot", got)
	}

	// Simulate a config with a supervised global default.
	m.config = &config.Config{DefaultMode: "supervised"}

	// Switch to a brand-new session (the /clear path).
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

// Issue #688 HIGH (resume branch, regression guard): resuming a session whose
// saved mode is autopilot must set BOTH the displayed mode and the policy mode.
func TestIssue688_ResumeSessionSyncsPolicyMode(t *testing.T) {
	m := newTestModel()
	cp := permission.NewConfigPolicy(nil, nil)
	m.policy = cp
	m.mode = cp.Mode()

	ses := newSes688()
	ses.PermissionMode = "autopilot"
	m.switchToSession(ses, false)

	if m.mode != permission.AutopilotMode {
		t.Errorf("displayed mode = %v, want autopilot", m.mode)
	}
	if got := cp.Mode(); got != permission.AutopilotMode {
		t.Errorf("policy mode = %v, want autopilot", got)
	}
}

// Issue #688 LOW: switching sessions (including /clear) must cancel pending
// approval/confirm state so the old session's requests cannot leak into the
// new session.
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

// Issue #688 LOW: changing the permission mode (Shift+Tab cycle and /mode
// command) must also drop pending approval state from the old mode's context.
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
