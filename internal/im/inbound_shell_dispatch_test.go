package im

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

func TestSubmitInboundMessage_ShellExecutesNotQueued(t *testing.T) {
	em := &captureEmitter{}
	allowDangerous := true
	b := &DaemonBridge{emitTextOverride: em.EmitText, remoteDangerousOverride: &allowDangerous}

	err := b.SubmitInboundMessage(context.Background(), InboundMessage{Text: "$ printf submitshell-ok"})
	if err != nil {
		t.Fatalf("SubmitInboundMessage: %v", err)
	}

	for i := 0; i < 150; i++ {
		em.mu.Lock()
		texts := append([]string(nil), em.texts...)
		em.mu.Unlock()
		for _, tx := range texts {
			if strings.Contains(tx, "submitshell-ok") {
				return
			}
		}
		sleepMs(20)
	}
	t.Fatal("shell command via SubmitInboundMessage never produced a result push (fell into the agent queue path)")
}

func TestSubmitInboundMessage_ShellWithPendingAskStillImmediate(t *testing.T) {

	em := &captureEmitter{}
	allowDangerous := true
	b := &DaemonBridge{emitTextOverride: em.EmitText, remoteDangerousOverride: &allowDangerous}
	b.mu.Lock()
	b.pendingAsk = &pendingAskUser{
		response: make(chan tool.AskUserResponse, 1),
	}
	b.mu.Unlock()

	err := b.SubmitInboundMessage(context.Background(), InboundMessage{Text: "$ printf askbusy-ok"})
	if err != nil {
		t.Fatalf("SubmitInboundMessage: %v", err)
	}

	for i := 0; i < 150; i++ {
		em.mu.Lock()
		texts := append([]string(nil), em.texts...)
		em.mu.Unlock()
		for _, tx := range texts {
			if strings.Contains(tx, "askbusy-ok") {
				return
			}
		}
		sleepMs(20)
	}
	t.Fatal("busy-agent shell passthrough did not execute immediately")
}

func TestSubmitInboundMessage_PlainTextStillQueues(t *testing.T) {

	em := &captureEmitter{}
	allowDangerous := true
	b := &DaemonBridge{emitTextOverride: em.EmitText, remoteDangerousOverride: &allowDangerous}

	func() {
		defer func() { _ = recover() }()
		_ = b.SubmitInboundMessage(context.Background(), InboundMessage{Text: "hello not a command"})
	}()

	sleepMs(300)
	em.mu.Lock()
	texts := append([]string(nil), em.texts...)
	em.mu.Unlock()
	for _, tx := range texts {
		if strings.Contains(tx, "hello not a command") && strings.Contains(tx, "exit") {
			t.Fatalf("plain text wrongly executed as shell: %s", tx)
		}
	}
}

var _ = sync.Mutex{}
