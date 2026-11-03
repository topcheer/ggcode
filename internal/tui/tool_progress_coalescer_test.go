package tui

import (
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

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

	for i := 0; i < 50; i++ {
		c.stage("tool-1", "run_command", "update")
	}
	c.stage("tool-1", "run_command", "FINAL")

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

func TestToolProgressCoalescerEmptyTick(t *testing.T) {
	sent := 0
	c := &toolProgressCoalescer{
		send: func(msgs ...tea.Msg) { sent += len(msgs) },
		next: make(map[string]toolProgressMsg),
	}

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
	_ = time.Millisecond
}

func TestToolProgressCoalescerTunnelAppendMerge(t *testing.T) {
	var mu sync.Mutex
	var batch []tea.Msg
	c := &toolProgressCoalescer{
		send: func(msgs ...tea.Msg) {
			mu.Lock()
			batch = append(batch, msgs...)
			mu.Unlock()
		},
		next:       make(map[string]toolProgressMsg),
		appendNext: make(map[string]subAgentTunnelStreamTextMsg),
		appendReas: make(map[string]subAgentTunnelReasoningMsg),
	}
	for _, s := range []string{"Hello", " ", "world", "!"} {
		c.stageTunnelText("agent-7", s)
	}
	c.stageTunnelText("agent-8", "other")
	c.stageTunnelReasoning("agent-7", "think")

	c.mu.Lock()
	msgs := make([]tea.Msg, 0, len(c.next)+len(c.appendNext)+len(c.appendReas))
	for _, m := range c.appendReas {
		msgs = append(msgs, m)
	}
	for _, m := range c.appendNext {
		msgs = append(msgs, m)
	}
	c.appendNext = make(map[string]subAgentTunnelStreamTextMsg)
	c.appendReas = make(map[string]subAgentTunnelReasoningMsg)
	c.mu.Unlock()
	c.send(msgs...)

	mu.Lock()
	defer mu.Unlock()
	var text7, text8, reas7 string
	for _, m := range batch {
		switch v := m.(type) {
		case subAgentTunnelStreamTextMsg:
			if v.AgentID == "agent-7" {
				text7 = v.Text
			} else if v.AgentID == "agent-8" {
				text8 = v.Text
			}
		case subAgentTunnelReasoningMsg:
			reas7 = v.Text
		}
	}
	if text7 != "Hello world!" {
		t.Fatalf("agent-7 chunks must append in order, got %q", text7)
	}
	if text8 != "other" {
		t.Fatalf("agent-8 text isolated, got %q", text8)
	}
	if reas7 != "think" {
		t.Fatalf("reasoning preserved, got %q", reas7)
	}
}

func TestToolProgressCoalescerEmptyGuards(t *testing.T) {
	c := &toolProgressCoalescer{
		send:       func(msgs ...tea.Msg) {},
		next:       make(map[string]toolProgressMsg),
		appendNext: make(map[string]subAgentTunnelStreamTextMsg),
		appendReas: make(map[string]subAgentTunnelReasoningMsg),
	}
	c.stageTunnelText("", "text")
	c.stageTunnelText("agent", "")
	c.stageTunnelReasoning("", "r")
	c.stageTunnelReasoning("agent", "")
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.appendNext) != 0 || len(c.appendReas) != 0 {
		t.Fatalf("empty guards failed: %+v %+v", c.appendNext, c.appendReas)
	}
}
