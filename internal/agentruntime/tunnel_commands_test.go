package agentruntime

import (
	"encoding/json"
	"testing"

	"github.com/topcheer/ggcode/internal/tunnel"
)

func TestRouteTunnelCommandUserMessageNormalizesMessageIDAndAcks(t *testing.T) {
	payload, err := json.Marshal(tunnel.MessageData{
		MessageID: " user-1 ",
		Text:      "hello",
	})
	if err != nil {
		t.Fatal(err)
	}

	var gotData tunnel.MessageData
	var ackID string
	RouteTunnelCommand(tunnel.GatewayMessage{Type: tunnel.CmdMessage, Data: payload}, TunnelCommandHooks{
		OnUserMessage: func(data tunnel.MessageData) {
			gotData = data
		},
		OnServerAck: func(messageID string) {
			ackID = messageID
		},
	})

	if gotData.Text != "hello" || gotData.MessageID != "user-1" {
		t.Fatalf("unexpected routed message data: %#v", gotData)
	}
	if ackID != "user-1" {
		t.Fatalf("ackID = %q, want user-1", ackID)
	}
}

func TestRouteTunnelCommandApprovalResponse(t *testing.T) {
	payload, err := json.Marshal(tunnel.ApprovalResponseData{ID: "req-1", Decision: tunnel.DecisionAllow})
	if err != nil {
		t.Fatal(err)
	}
	var got tunnel.ApprovalResponseData
	RouteTunnelCommand(tunnel.GatewayMessage{Type: tunnel.CmdApprovalResponse, Data: payload}, TunnelCommandHooks{
		OnApprovalResponse: func(data tunnel.ApprovalResponseData) {
			got = data
		},
	})
	if got.ID != "req-1" || got.Decision != tunnel.DecisionAllow {
		t.Fatalf("unexpected approval response: %#v", got)
	}
}
