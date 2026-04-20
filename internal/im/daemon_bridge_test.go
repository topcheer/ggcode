package im

import (
	"context"
	"testing"

	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

func TestDaemonBridgeSubmitInboundMessageTriggersActivityHook(t *testing.T) {
	bridge := &DaemonBridge{}
	called := 0
	bridge.SetActivityHook(func() {
		called++
	})
	bridge.pendingAsk = &pendingAskUser{
		request: toolpkg.AskUserRequest{
			Questions: []toolpkg.AskUserQuestion{{ID: "q1", Title: "Question"}},
		},
		response: make(chan toolpkg.AskUserResponse, 1),
	}

	if err := bridge.SubmitInboundMessage(context.Background(), InboundMessage{Text: "answer"}); err != nil {
		t.Fatalf("SubmitInboundMessage returned error: %v", err)
	}
	if called != 1 {
		t.Fatalf("expected activity hook once, got %d", called)
	}
}
