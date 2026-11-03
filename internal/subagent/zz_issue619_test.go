package subagent

import (
	"context"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
)

func startStuckAgent(mgr *Manager, name string) string {
	id := mgr.Spawn(name, "task", "", nil, context.Background())
	sa, _ := mgr.Get(id)
	_, cancel := context.WithCancel(context.Background())
	mgr.SetCancel(id, cancel)
	sa.mu.Lock()
	sa.goroutineStarted = true
	sa.mu.Unlock()
	if err := mgr.acquireSlot(context.Background(), id); err != nil {
		panic("test setup: acquireSlot failed: " + err.Error())
	}
	return id
}

func TestIssue619_CancelAllForceReclaimsLeakedSlot(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 2, Timeout: time.Second})
	mgr.cancelAllTimeout = 100 * time.Millisecond

	id1 := startStuckAgent(mgr, "z1")
	id2 := startStuckAgent(mgr, "z2")

	done := make(chan struct{})
	go func() { mgr.CancelAll(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("CancelAll did not return")
	}

	acquired := make(chan error, 1)
	go func() { acquired <- mgr.acquireSlot(context.Background(), "sa-new") }()
	select {
	case err := <-acquired:
		if err != nil {
			t.Fatalf("new agent could not acquire reclaimed slot: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("semaphore slot leaked: new agent blocked on acquire (permanently Pending)")
	}

	relDone := make(chan struct{})
	go func() { mgr.releaseSlot(id1); close(relDone) }()
	select {
	case <-relDone:
	case <-time.After(time.Second):
		t.Fatal("late releaseSlot blocked after force reclaim")
	}
	_ = id2
}

func TestIssue619_CancelAllPerAgentTimeoutBudget(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 5, Timeout: time.Second})
	mgr.cancelAllTimeout = 120 * time.Millisecond

	startStuckAgent(mgr, "A")
	startStuckAgent(mgr, "B")

	start := time.Now()
	n := mgr.CancelAll()
	elapsed := time.Since(start)

	if n != 2 {
		t.Fatalf("expected 2 cancelled, got %d", n)
	}
	if elapsed < 2*120*time.Millisecond-40*time.Millisecond {
		t.Fatalf("per-agent timeout budget not honored: CancelAll returned after %v, want ~%v", elapsed, 2*120*time.Millisecond)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("CancelAll took too long: %v", elapsed)
	}
}

func TestIssue620_ToolCallChunkRefreshesActivity(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{})

	stale := time.Now().Add(-10 * time.Minute)

	idActive := mgr.Spawn("active", "t", "", nil, context.Background())
	saActive, _ := mgr.Get(idActive)
	saActive.setStatus(StatusRunning)
	saActive.mu.Lock()
	saActive.goroutineStarted = true
	saActive.lastActivity = stale
	saActive.mu.Unlock()
	saActive.refreshActivity()

	idDead := mgr.Spawn("dead", "t", "", nil, context.Background())
	saDead, _ := mgr.Get(idDead)
	saDead.setStatus(StatusRunning)
	saDead.mu.Lock()
	saDead.goroutineStarted = true
	saDead.lastActivity = stale
	saDead.mu.Unlock()

	mgr.reapInactiveAgents()

	if got := saActive.getStatus(); got != StatusRunning {
		t.Fatalf("watchdog killed agent streaming tool-arg chunks: status=%s", got)
	}
	if got := saDead.getStatus(); got != StatusCancelled {
		t.Fatalf("control agent should have been reaped, status=%s", got)
	}
}

func TestIssue621_SuccessCompleteAfterCancelKeepsResult(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{})
	id := mgr.Spawn("probe", "task", "", nil, context.Background())

	sa, _ := mgr.Get(id)
	sa.mu.Lock()
	sa.Status = StatusCancelled
	sa.Error = context.Canceled
	sa.mu.Unlock()

	mgr.Complete(id, "FULL SUCCESS RESULT", nil)

	snap, ok := mgr.Snapshot(id)
	if !ok {
		t.Fatal("snapshot not found")
	}
	if snap.Result != "FULL SUCCESS RESULT" {
		t.Fatalf("success result dropped on cancel race: got %q", snap.Result)
	}
	if snap.Status != StatusCancelled {
		t.Fatalf("terminal status overwritten: %s", snap.Status)
	}
	if snap.Error != context.Canceled.Error() {
		t.Fatalf("cancel error overwritten: %q", snap.Error)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := Wait(ctx, mgr, id)
	if res != "FULL SUCCESS RESULT" {
		t.Fatalf("Wait lost recovered result: %q", res)
	}
	if err == nil {
		t.Fatal("expected cancellation error from Wait")
	}
}

type issue622Runner struct{}

func (r *issue622Runner) RunStream(ctx context.Context, prompt string, onEvent func(provider.StreamEvent), origin ...string) error {
	onEvent(provider.StreamEvent{
		Type: provider.StreamEventToolCallDone,
		Tool: provider.ToolCallDelta{Name: "toolA", Arguments: []byte(`{"a":1}`)},
	})
	onEvent(provider.StreamEvent{
		Type: provider.StreamEventToolCallDone,
		Tool: provider.ToolCallDelta{Name: "toolB", Arguments: []byte(`{"b":2}`)},
	})
	onEvent(provider.StreamEvent{Type: provider.StreamEventToolResult, Tool: provider.ToolCallDelta{}, Result: "r1"})
	onEvent(provider.StreamEvent{Type: provider.StreamEventToolResult, Tool: provider.ToolCallDelta{}, Result: "r2"})
	return nil
}

func TestIssue622_UnnamedToolFIFOPairing(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 2, Timeout: time.Second})
	id := mgr.Spawn("probe", "task", "", nil, context.Background())

	Run(context.Background(), RunnerConfig{
		Task:       "task",
		Manager:    mgr,
		SubAgentID: id,
		AgentFactory: func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) AgentRunner {
			return &issue622Runner{}
		},
	})

	sa, _ := mgr.Get(id)
	var results []AgentEvent
	for _, ev := range sa.Events() {
		if ev.Type == AgentEventToolResult {
			results = append(results, ev)
		}
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 tool result events, got %d", len(results))
	}
	if results[0].ToolName != "toolA" || results[0].ToolArgs != `{"a":1}` {
		t.Fatalf("first unnamed result mismatched: name=%q args=%q", results[0].ToolName, results[0].ToolArgs)
	}
	if results[1].ToolName != "toolB" || results[1].ToolArgs != `{"b":2}` {
		t.Fatalf("second unnamed result mismatched: name=%q args=%q", results[1].ToolName, results[1].ToolArgs)
	}
}
