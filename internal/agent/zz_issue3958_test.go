package agent

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/tool"
)

func TestIssue3958IsAutopilotModeGetter(t *testing.T) {
	a := NewAgent(nil, tool.NewRegistry(), "", 10)
	defer a.Close()

	if a.IsAutopilotMode() {
		t.Fatal("default mode must not report autopilot")
	}

	a.policy = permission.NewConfigPolicyWithMode(nil, nil, permission.AutopilotMode)
	if !a.IsAutopilotMode() {
		t.Fatal("autopilot policy must report IsAutopilotMode=true")
	}

	got := EffectiveSessionTimeout(0, a.IsAutopilotMode())
	if got != defaultAutopilotSessionTimeout {
		t.Fatalf("EffectiveSessionTimeout(0, IsAutopilotMode()) = %v, want %v", got, defaultAutopilotSessionTimeout)
	}

	a.policy = permission.NewConfigPolicyWithMode(nil, nil, permission.SupervisedMode)
	if a.IsAutopilotMode() {
		t.Fatal("supervised policy must report IsAutopilotMode=false")
	}
	if got := EffectiveSessionTimeout(0, a.IsAutopilotMode()); got != 0 {
		t.Fatalf("supervised with unset timeout = %v, want 0", got)
	}

	a.policy = permission.NewConfigPolicyWithMode(nil, nil, permission.AutopilotMode)
	if got := EffectiveSessionTimeout(90*time.Minute, a.IsAutopilotMode()); got != 90*time.Minute {
		t.Fatalf("explicit timeout must win, got %v", got)
	}
}

func TestIssue3958GetterNilSafe(t *testing.T) {
	var a *Agent
	if a.IsAutopilotMode() {
		t.Fatal("nil agent must report false, not panic")
	}
}
