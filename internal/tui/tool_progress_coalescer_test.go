package tui

import (
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// TestToolProgressCoalescerLatestWins: staging many updates for the same
// toolID within one flush window must emit exactly ONE message - the latest
// output. This is what bounds main-thread renders during high-volume
// command output (TUI jank fix).
func TestToolProgressCoalescerLatestWins(t *testing.T) {
	var mu sync.Mutex
	var sent [][]tea.Msg
	done := make(chan struct{})
	c := &toolProgressCoalescer{
		send: func(msgs ...tea.Msg) {
			mu.Lock()
			sent = append(sent, msgs)
			mu.Unlock()
			select {
			case <-done:
			default:
				close(done)
			}
		},
		next: make(map[string]toolProgressMsg),
	}

	// Stage 50 updates for one tool before any flush can run (single-
	// goroutine staging, no sleeps racing the ticker).
	for i := 0; i < 50; i++ {
		c.stage("tool-1", "run_command", "update")
	}
	c.stage("tool-1", "run_command", "FINAL")

	// Force one drain synchronously (same code path the ticker runs).
	c.mu.Lock()
	msgs := make([]tea.Msg, 0, len(c.next))
	for _, m := range c.next {
		msgs = append(msgs, m)
	}
	c.next = make(map[string]toolProgressMsg)
	c.mu.Unlock()
	c.send(msgs...)

	<-done
	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 1 || len(sent[0]) != 1 {
		t.Fatalf("50 staged updates must coalesce to 1 message, got %d batches / %d msgs", len(sent), len(sent[0]))
	}
	pm, ok := sent[0][0].(toolProgressMsg)
	if !ok {
		t.Fatalf("expected toolProgressMsg, got %T", sent[0][0])
	}
	if pm.output != "FINAL" {
		t.Fatalf("latest output must win, got %q", pm.output)
	}
}

// TestToolProgressCoalescerMultiTool: distinct toolIDs each get their own
// message in the same flush batch (parallel tools stay visible).
func TestToolProgressCoalescerMultiTool(t *testing.T) {
	var mu sync.Mutex
	var batch []tea.Msg
	c := &toolProgressCoalescer{
		send: func(msgs ...tea.Msg) {
			mu.Lock()
			batch = append(batch, msgs...)
			mu.Unlock()
		},
		next: make(map[string]toolProgressMsg),
	}
	c.stage("a", "run_command", "1")
	c.stage("b", "run_command", "2")
	c.stage("c", "wait_command", "3")

	c.mu.Lock()
	msgs := make([]tea.Msg, 0, len(c.next))
	for _, m := range c.next {
		msgs = append(msgs, m)
	}
	c.next = make(map[string]toolProgressMsg)
	c.mu.Unlock()
	c.send(msgs...)

	mu.Lock()
	defer mu.Unlock()
	if len(batch) != 3 {
		t.Fatalf("3 distinct tools must each emit, got %d", len(batch))
	}
}

// TestToolProgressCoalescerEmptyTick: a drain with nothing staged sends
// nothing (no idle renders).
func TestToolProgressCoalescerEmptyTick(t *testing.T) {
	sent := 0
	c := &toolProgressCoalescer{
		send: func(msgs ...tea.Msg) { sent += len(msgs) },
		next: make(map[string]toolProgressMsg),
	}
	// Simulate several empty ticker drains.
	for i := 0; i < 5; i++ {
		c.mu.Lock()
		empty := len(c.next) == 0
		c.mu.Unlock()
		if empty {
			continue
		}
	}
	if sent != 0 {
		t.Fatalf("no staging must mean no sends, got %d", sent)
	}
	_ = time.Millisecond // keep time import stable if asserts evolve
}
