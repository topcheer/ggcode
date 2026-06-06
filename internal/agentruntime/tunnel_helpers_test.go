package agentruntime

import (
	"testing"

	"github.com/topcheer/ggcode/internal/tunnel"
)

func TestTunnelReasoningMsgID(t *testing.T) {
	if got := TunnelReasoningMsgID("msg-1"); got != "msg-1-reasoning" {
		t.Fatalf("TunnelReasoningMsgID() = %q", got)
	}
	if got := TunnelReasoningMsgID(""); got != "" {
		t.Fatalf("TunnelReasoningMsgID(empty) = %q", got)
	}
}

func TestMarkTunnelMainStreamActiveRequiresMessageID(t *testing.T) {
	state := MarkTunnelMainStreamActive(TunnelMainStream{})
	if state.NeedsFinalize {
		t.Fatal("expected empty stream to stay inactive")
	}
	state = MarkTunnelMainStreamActive(TunnelMainStream{MessageID: "msg-1"})
	if !state.NeedsFinalize {
		t.Fatal("expected non-empty stream to mark finalize")
	}
}

func TestAttachTunnelBrokerSetsBrokerState(t *testing.T) {
	broker := tunnel.NewBroker(nil)
	defer broker.Stop()

	status := tunnel.StatusData{Status: tunnel.StatusBusy, Message: "working"}
	activity := "processing"
	cfg := TunnelAttachConfig{
		ReplayProvider: func() []tunnel.GatewayMessage {
			return []tunnel.GatewayMessage{{EventID: "ev-1", Type: tunnel.EventText}}
		},
		SessionID:      "sess-1",
		AuthorityEpoch: 7,
		SessionInfo:    &tunnel.SessionInfoData{Workspace: "/tmp/repo", Model: "glm", Provider: "zai", Mode: "default"},
		Status:         &status,
		Activity:       &activity,
	}

	AttachTunnelBroker(broker, cfg)

	if got := broker.SessionID(); got != "sess-1" {
		t.Fatalf("SessionID() = %q", got)
	}
	if got := broker.AuthorityEpoch(); got != 7 {
		t.Fatalf("AuthorityEpoch() = %d", got)
	}
	gotStatus, ok := broker.CurrentStatus()
	if !ok || gotStatus.Status != tunnel.StatusBusy || gotStatus.Message != "working" {
		t.Fatalf("CurrentStatus() = %+v, ok=%v", gotStatus, ok)
	}
	gotActivity, ok := broker.CurrentActivity()
	if !ok || gotActivity.Activity != "processing" {
		t.Fatalf("CurrentActivity() = %+v, ok=%v", gotActivity, ok)
	}
}

func TestFlushTunnelMainStreamRotatesMessageID(t *testing.T) {
	broker := tunnel.NewBroker(nil)
	defer broker.Stop()

	current := broker.NextMessageID()
	state := TunnelMainStream{MessageID: current, NeedsFinalize: true}
	next := FlushTunnelMainStream(state, broker, false)
	if next.MessageID == "" || next.MessageID == current {
		t.Fatalf("expected rotated message id, got %+v", next)
	}
	if next.NeedsFinalize {
		t.Fatalf("expected flush to clear finalize flag, got %+v", next)
	}
}

func TestFlushTunnelMainStreamSkipsInactiveStream(t *testing.T) {
	broker := tunnel.NewBroker(nil)
	defer broker.Stop()

	state := TunnelMainStream{MessageID: "msg-1", NeedsFinalize: false}
	next := FlushTunnelMainStream(state, broker, false)
	if next != state {
		t.Fatalf("expected unchanged state, got %+v", next)
	}
}
