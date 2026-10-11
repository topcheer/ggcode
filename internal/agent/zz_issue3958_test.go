package agent

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/tool"
)

// #3958: the agentruntime reload paths used to call ApplySessionTimeout with
// isAutopilot hardcoded to false, so any mid-run config reload overwrote the
// 600-min autopilot wall-clock default with 0 (disabled) while the run itself
// had armed start(true). The fix routes the REAL mode through a new exported
// getter; these probes pin the getter's semantics and its interplay with
// EffectiveSessionTimeout.
// The permission package ships a mode-aware concrete policy
// (ConfigPolicy via NewConfigPolicyWithMode); use the real thing so the
// getter is probed against production wiring, not a hand-rolled fake.

func TestIssue3958IsAutopilotModeGetter(t *testing.T) {
	a := NewAgent(nil, tool.NewRegistry(), "", 10)
	defer a.Close()

	// Default agent (no mode-aware policy): supervised, not autopilot.
	if a.IsAutopilotMode() {
		t.Fatal("default mode must not report autopilot")
	}

	// Mode-aware policy in autopilot: getter must report true.
	a.policy = permission.NewConfigPolicyWithMode(nil, nil, permission.AutopilotMode)
	if !a.IsAutopilotMode() {
		t.Fatal("autopilot policy must report IsAutopilotMode=true")
	}

	// ...and the reload wiring expression derives the 600-min default,
	// not 0 (the #3958 bug: hardcoded false => EffectiveSessionTimeout(0,false)=0).
	got := EffectiveSessionTimeout(0, a.IsAutopilotMode())
	if got != defaultAutopilotSessionTimeout {
		t.Fatalf("EffectiveSessionTimeout(0, IsAutopilotMode()) = %v, want %v", got, defaultAutopilotSessionTimeout)
	}

	// Mode switched back to supervised mid-run: getter tracks the live mode.
	a.policy = permission.NewConfigPolicyWithMode(nil, nil, permission.SupervisedMode)
	if a.IsAutopilotMode() {
		t.Fatal("supervised policy must report IsAutopilotMode=false")
	}
	if got := EffectiveSessionTimeout(0, a.IsAutopilotMode()); got != 0 {
		t.Fatalf("supervised with unset timeout = %v, want 0", got)
	}

	// An explicitly configured timeout wins regardless of mode (effectiveTimeout contract).
	a.policy = permission.NewConfigPolicyWithMode(nil, nil, permission.AutopilotMode)
	if got := EffectiveSessionTimeout(90*time.Minute, a.IsAutopilotMode()); got != 90*time.Minute {
		t.Fatalf("explicit timeout must win, got %v", got)
	}
}

// Nil-receiver safety: the agentruntime call sites may hold a nil agentInst
// (ApplySessionTimeout nil-checks it, but the getter argument is evaluated first).
func TestIssue3958GetterNilSafe(t *testing.T) {
	var a *Agent
	if a.IsAutopilotMode() {
		t.Fatal("nil agent must report false, not panic")
	}
}
