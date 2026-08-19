package im

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

// Integration-level regression: the 429075b0 commit shipped the shell route
// classifier and handler but MISSED the dispatch wiring in
// SubmitInboundMessage (only an emitTextOverride field landed), so $ commands
// from IM fell through to the agent queue exactly as before. These tests go
// through the full SubmitInboundMessage path so a missing dispatch branch
// cannot slip through again.

func TestSubmitInboundMessage_ShellExecutesNotQueued(t *testing.T) {
	em := &captureEmitter{}
	b := &DaemonBridge{emitTextOverride: em.EmitText}

	err := b.SubmitInboundMessage(context.Background(), InboundMessage{Text: "$ printf submitshell-ok"})
	if err != nil {
		t.Fatalf("SubmitInboundMessage: %v", err)
	}

	// handleShellInbound runs async; poll for the pushed result.
	for i := 0; i < 150; i++ {
		em.mu.Lock()
		texts := append([]string(nil), em.texts...)
		em.mu.Unlock()
		for _, tx := range texts {
			if strings.Contains(tx, "submitshell-ok") {
				return // pass: executed and pushed, not queued for the agent
			}
		}
		sleepMs(20)
	}
	t.Fatal("shell command via SubmitInboundMessage never produced a result push (fell into the agent queue path)")
}

func TestSubmitInboundMessage_ShellWithPendingAskStillImmediate(t *testing.T) {
	// Agent busy + ask_user pending: $ cmd must STILL route to shell, not be
	// swallowed by the ask-reply channel.
	em := &captureEmitter{}
	b := &DaemonBridge{emitTextOverride: em.EmitText}
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
				return // pass
			}
		}
		sleepMs(20)
	}
	t.Fatal("busy-agent shell passthrough did not execute immediately")
}

func TestSubmitInboundMessage_PlainTextStillQueues(t *testing.T) {
	// Guard the other direction: ordinary text must NOT hit the shell path.
	// The bare bridge has no agent loop, so the queue path may panic/nil-deref
	// — recover it; the assertion is only that no shell pushback happened.
	em := &captureEmitter{}
	b := &DaemonBridge{emitTextOverride: em.EmitText}

	func() {
		defer func() { _ = recover() }()
		_ = b.SubmitInboundMessage(context.Background(), InboundMessage{Text: "hello not a command"})
	}()

	sleepMs(300) // well past shell exec latency
	em.mu.Lock()
	texts := append([]string(nil), em.texts...)
	em.mu.Unlock()
	for _, tx := range texts {
		if strings.Contains(tx, "hello not a command") && strings.Contains(tx, "exit") {
			t.Fatalf("plain text wrongly executed as shell: %s", tx)
		}
	}
}

var _ = sync.Mutex{} // keep sync import stable if assertions change
