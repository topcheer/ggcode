package agentruntime

import (
	"testing"

	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tunnel"
)

func TestResolveTunnelApprovalAppliesAlwaysAllowOverride(t *testing.T) {
	var gotTool string
	decision := ResolveTunnelApproval(tunnel.DecisionAlwaysAllow, "bash", func(toolName string) {
		gotTool = toolName
	})
	if decision != permission.Allow {
		t.Fatalf("decision = %v", decision)
	}
	if gotTool != "bash" {
		t.Fatalf("override tool = %q", gotTool)
	}
}

func TestPushTunnelAskUserResponseRequiresID(t *testing.T) {
	broker := tunnel.NewBroker(nil)
	defer broker.Stop()

	PushTunnelAskUserResponse(broker, "", tool.AskUserResponse{Status: tool.AskUserStatusSubmitted}, TunnelStateUpdate{
		HasStatus: true,
		Status:    tunnel.StatusBusy,
	})

	if status, ok := broker.CurrentStatus(); ok || status.Status != "" {
		t.Fatalf("expected no status update without request id, got %+v ok=%v", status, ok)
	}
}

func TestApplyTunnelStateUpdateSetsStatusAndActivity(t *testing.T) {
	broker := tunnel.NewBroker(nil)
	defer broker.Stop()

	ApplyTunnelStateUpdate(broker, TunnelStateUpdate{
		HasStatus:   true,
		Status:      tunnel.StatusBusy,
		StatusMsg:   "working",
		HasActivity: true,
		Activity:    "approval",
	})

	status, ok := broker.CurrentStatus()
	if !ok || status.Status != tunnel.StatusBusy || status.Message != "working" {
		t.Fatalf("status = %+v ok=%v", status, ok)
	}
	activity, ok := broker.CurrentActivity()
	if !ok || activity.Activity != "approval" {
		t.Fatalf("activity = %+v ok=%v", activity, ok)
	}
}
