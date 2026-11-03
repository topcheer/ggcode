package wailskit

import (
	"github.com/topcheer/ggcode/internal/provider"
	"strings"
	"testing"
)

// TestPendingSlicesInterruptDrainSync (#477): drainPendingInterrupt consumes
// a VISIBLE queued message at the agent iteration boundary — the parallel
// pendingSource/pendingExclude slices must be popped in the same step, or
// every later defer-drain shifts by one (desktop message misattributed
// source=im, Telegram misses echoes, slices grow unboundedly).
func TestPendingSlicesInterruptDrainSync(t *testing.T) {
	b, err := NewChatBridge()
	if err != nil {
		t.Skipf("bridge init failed in test env: %v", err)
	}

	b.mu.Lock()
	b.pendingMsgs.Enqueue("from telegram", false, nil)
	b.pendingSource = append(b.pendingSource, "im")
	b.pendingExclude = append(b.pendingExclude, "telegram")
	b.mu.Unlock()

	if got := b.drainPendingInterrupt(); !strings.Contains(providerBlocksText(got), "from telegram") {
		t.Fatalf("interrupt drain should return the queued text, got %+v", got)
	}
	b.mu.Lock()
	srcLen, exclLen := len(b.pendingSource), len(b.pendingExclude)
	b.mu.Unlock()
	if srcLen != 0 || exclLen != 0 {
		t.Fatalf("parallel slices must be empty after interrupt consume, got source=%d exclude=%d", srcLen, exclLen)
	}

	b.mu.Lock()
	b.pendingMsgs.Enqueue("from desktop", false, nil)
	b.pendingSource = append(b.pendingSource, "desktop")
	b.pendingExclude = append(b.pendingExclude, "")
	b.mu.Unlock()

	pending, ok := b.drainPending()
	if !ok || pending.Text != "from desktop" {
		t.Fatalf("defer drain should get the desktop message, ok=%v", ok)
	}
	b.mu.Lock()
	src := b.pendingSource[0]
	excl := b.pendingExclude[0]
	b.pendingSource = b.pendingSource[1:]
	b.pendingExclude = b.pendingExclude[1:]
	b.mu.Unlock()
	if src != "desktop" || excl != "" {
		t.Fatalf("desktop message must drain with its OWN pair, got source=%q exclude=%q (stale im/telegram leaked)", src, excl)
	}
}

// TestQueueMessageAppendsPair (#477): the QueueMessage entry point enqueues a
// VISIBLE message — it must append a parallel pair like the sendMessageData
// busy branch, or the FIFO alignment breaks from that entry too.
func TestQueueMessageAppendsPair(t *testing.T) {
	b, err := NewChatBridge()
	if err != nil {
		t.Skipf("bridge init failed in test env: %v", err)
	}

	b.QueueMessage("queued via entry point")

	b.mu.Lock()
	srcLen, exclLen := len(b.pendingSource), len(b.pendingExclude)
	b.mu.Unlock()
	if srcLen != 1 || exclLen != 1 {
		t.Fatalf("QueueMessage must append one parallel pair, got source=%d exclude=%d", srcLen, exclLen)
	}

	b.drainPendingInterrupt()
	b.mu.Lock()
	srcLen, exclLen = len(b.pendingSource), len(b.pendingExclude)
	b.mu.Unlock()
	if srcLen != 0 || exclLen != 0 {
		t.Fatalf("pair must be consumed with the visible message, got source=%d exclude=%d", srcLen, exclLen)
	}
}

// providerBlocksText flattens content blocks for substring assertions
// (#1472-A: drain returns blocks now).
func providerBlocksText(blocks []provider.ContentBlock) string {
	out := ""
	for _, b := range blocks {
		out += b.Text
	}
	return out
}
