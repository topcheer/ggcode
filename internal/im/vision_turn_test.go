package im

import (
	"testing"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/tool"
)

func newVisionTurnBridge(t *testing.T, agentVision bool, userModel string) (*DaemonBridge, *[]string) {
	t.Helper()
	mgr := NewManager()
	emitter := NewIMEmitter(mgr, "en", t.TempDir())
	prov := &daemonBridgeMetricsProvider{}
	registry := tool.NewRegistry()
	ag := agent.NewAgent(prov, registry, "vision-turn-test", 1)
	ag.SetPermissionPolicy(permission.NewConfigPolicyWithMode(nil, []string{t.TempDir()}, permission.SupervisedMode))
	ag.SetSupportsVision(agentVision)

	var ses *session.Session
	if userModel != "" {
		ses = &session.Session{ID: "vision-turn", Model: userModel}
	}
	bridge := NewDaemonBridge(mgr, ag, emitter, nil, ses)

	calls := &[]string{}
	bridge.SetVisionTurnHook(
		func() string { return "vision-model" },
		func(model string) error {
			*calls = append(*calls, model)
			return nil
		})
	return bridge, calls
}

func TestBeginVisionTurn_SwitchAndRestore(t *testing.T) {
	bridge, calls := newVisionTurnBridge(t, false, "user-model")
	content := []provider.ContentBlock{
		{Type: "text", Text: "hello"},
		{Type: "image", ImageMIME: "image/png"},
	}

	restore := bridge.beginVisionTurn(content)
	if len(*calls) != 1 || (*calls)[0] != "vision-model" {
		t.Fatalf("expected switch to vision-model, got %v", *calls)
	}
	restore()
	restore() // idempotent
	if len(*calls) != 2 || (*calls)[1] != "user-model" {
		t.Fatalf("expected restore to user-model, got %v", *calls)
	}
}

func TestBeginVisionTurn_NoImageNoSwitch(t *testing.T) {
	bridge, calls := newVisionTurnBridge(t, false, "user-model")
	restore := bridge.beginVisionTurn([]provider.ContentBlock{{Type: "text", Text: "plain"}})
	restore()
	if len(*calls) != 0 {
		t.Fatalf("expected no switch for text-only content, got %v", *calls)
	}
}

func TestBeginVisionTurn_VisionAgentNoSwitch(t *testing.T) {
	bridge, calls := newVisionTurnBridge(t, true, "user-model")
	restore := bridge.beginVisionTurn([]provider.ContentBlock{{Type: "image", ImageMIME: "image/png"}})
	restore()
	if len(*calls) != 0 {
		t.Fatalf("expected no switch when agent already has vision, got %v", *calls)
	}
}

func TestBeginVisionTurn_NoSessionKeepsSwitchUntilNoop(t *testing.T) {
	// Without a session there is no user model to restore to: the switch
	// still happens for the turn, restore is a no-op.
	bridge, calls := newVisionTurnBridge(t, false, "")
	restore := bridge.beginVisionTurn([]provider.ContentBlock{{Type: "image", ImageMIME: "image/png"}})
	if len(*calls) != 1 {
		t.Fatalf("expected turn switch, got %v", *calls)
	}
	restore()
	if len(*calls) != 1 {
		t.Fatalf("restore must be a no-op without a session model, got %v", *calls)
	}
}
