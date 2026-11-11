package tui

import (
	"testing"

	"github.com/topcheer/ggcode/internal/permission"
)

// TestIssue5454TunnelModeChangePublishesLiveState pins the HIGH residual the
// QA review found after PR #5462: handleTunnelModeChangeMsg (mobile-origin
// mode switch) wrote m.mode on the bubbletea copy without publishing to the
// modeLive cross-copy store, so webui RuntimeStatus kept reporting the OLD
// mode after a tunnel/mobile switch - the exact #5454 symptom on a different
// origin. The handler runs on the value copy tea.NewProgram semantics imply;
// the assertion reads the REPL-held original, like RuntimeStatus does.
func TestIssue5454TunnelModeChangePublishesLiveState(t *testing.T) {
	orig := NewModel(nil, nil)
	orig.mode = permission.SupervisedMode
	orig.modeLive.set(permission.SupervisedMode)

	copyM := orig // the value copy bubbletea evolves
	copyM.handleTunnelModeChangeMsg(tunnelModeChangeMsg{mode: "plan"})

	if orig.mode == permission.PlanMode {
		t.Fatal("premise broken: original mode mutated through the copy")
	}
	got, ok := orig.modeLive.get()
	if !ok || got != permission.PlanMode {
		t.Fatalf("tunnel/mobile mode switch did not reach the cross-copy store: got=%s ok=%v (webui would keep reporting %s)", got, ok, orig.mode)
	}
}

// TestIssue5454TunnelModeChangeInvalidModeKeepsLiveState: invalid mode
// strings are rejected before any publish - the live store must not drift.
func TestIssue5454TunnelModeChangeInvalidModeKeepsLiveState(t *testing.T) {
	orig := NewModel(nil, nil)
	orig.mode = permission.SupervisedMode
	orig.modeLive.set(permission.SupervisedMode)

	copyM := orig
	copyM.handleTunnelModeChangeMsg(tunnelModeChangeMsg{mode: "not-a-mode"})

	got, ok := orig.modeLive.get()
	if !ok || got != permission.SupervisedMode {
		t.Fatalf("invalid mode leaked into the live store: got=%s ok=%v", got, ok)
	}
}

// TestIssue5454RuntimeToolReadsLiveState pins the MEDIUM residual: the
// agent-facing runtime_status tool read the stale REPL-held copy
// (tuiRuntimeProvider.RuntimePermissionMode), so after a handler switched
// the mode the agent was still told the OLD mode. The provider must prefer
// the cross-copy store exactly like the webui reader (liveModeString).
func TestIssue5454RuntimeToolReadsLiveState(t *testing.T) {
	r := &REPL{model: Model{mode: permission.PlanMode, modeLive: &modeLiveState{}}}
	r.model.modeLive.set(permission.PlanMode)

	// A bubbletea handler mutates its copy and publishes to the shared store.
	handlerCopy := r.model
	handlerCopy.mode = permission.BypassMode
	handlerCopy.modeLive.set(permission.BypassMode)

	p := &tuiRuntimeProvider{repl: r}
	if got := p.RuntimePermissionMode(); got != permission.BypassMode.String() {
		t.Fatalf("runtime_status tool would report the stale boot-time mode: got=%s want=%s", got, permission.BypassMode)
	}
}
