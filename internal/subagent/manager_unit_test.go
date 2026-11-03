//go:build !integration

package subagent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

func TestManagerSpawnIncrementingIDs(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 5})
	id1 := mgr.Spawn("a", "task1", "display1", nil, context.Background())
	id2 := mgr.Spawn("b", "task2", "display2", nil, context.Background())
	if id1 == id2 {
		t.Errorf("expected different IDs, got %q for both", id1)
	}
	if id1 != "sa-1" {
		t.Errorf("expected sa-1, got %q", id1)
	}
	if id2 != "sa-2" {
		t.Errorf("expected sa-2, got %q", id2)
	}
}

func TestManagerSpawnFields(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 5})
	tools := []string{"read_file", "edit_file"}
	id := mgr.Spawn("my-agent", "do something", "display task", tools, context.Background())

	sa, ok := mgr.Get(id)
	if !ok {
		t.Fatalf("agent %q not found", id)
	}
	if sa.Name != "my-agent" {
		t.Errorf("Name = %q, want 'my-agent'", sa.Name)
	}
	if sa.Task != "do something" {
		t.Errorf("Task = %q, want 'do something'", sa.Task)
	}
	if sa.DisplayTask != "display task" {
		t.Errorf("DisplayTask = %q, want 'display task'", sa.DisplayTask)
	}
	if len(sa.Tools) != 2 || sa.Tools[0] != "read_file" {
		t.Errorf("Tools = %v, want [read_file edit_file]", sa.Tools)
	}
	if sa.Status != StatusPending {
		t.Errorf("Status = %q, want pending", sa.Status)
	}
	if sa.CurrentPhase != "pending" {
		t.Errorf("CurrentPhase = %q, want pending", sa.CurrentPhase)
	}
	if sa.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}
	if sa.Mailbox == nil {
		t.Error("Mailbox should not be nil")
	}
}

func TestManagerGetNonexistent(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{})
	_, ok := mgr.Get("nonexistent")
	if ok {
		t.Error("expected false for nonexistent agent")
	}
}

func TestManagerSnapshotNotFound(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{})
	_, ok := mgr.Snapshot("nonexistent")
	if ok {
		t.Error("expected false for nonexistent snapshot")
	}
}

func TestManagerSnapshotFields(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 5})
	id := mgr.Spawn("snap-agent", "task", "display", nil, context.Background())
	snap, ok := mgr.Snapshot(id)
	if !ok {
		t.Fatal("expected snapshot to exist")
	}
	if snap.Name != "snap-agent" {
		t.Errorf("Name = %q", snap.Name)
	}
	if snap.Status != StatusPending {
		t.Errorf("Status = %q", snap.Status)
	}
	if len(snap.Events) != 0 {
		t.Errorf("Events should be empty, got %d", len(snap.Events))
	}
}

func TestManagerListEmpty(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{})
	list := mgr.List()
	if len(list) != 0 {
		t.Errorf("expected empty list, got %d items", len(list))
	}
}

func TestManagerListMultiple(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 5})
	mgr.Spawn("a", "t1", "", nil, context.Background())
	mgr.Spawn("b", "t2", "", nil, context.Background())
	list := mgr.List()
	if len(list) != 2 {
		t.Errorf("expected 2 agents, got %d", len(list))
	}
}

func TestManagerRunningCountZero(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{})
	if count := mgr.RunningCount(); count != 0 {
		t.Errorf("expected 0 running, got %d", count)
	}
}

func TestManagerRunningCountAfterSetCancel(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 5})
	id := mgr.Spawn("a", "task", "", nil, context.Background())

	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.SetCancel(id, cancel)

	if count := mgr.RunningCount(); count != 1 {
		t.Errorf("expected 1 running after SetCancel, got %d", count)
	}
}

func TestManagerCancelNonexistent(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{})
	if mgr.Cancel("nonexistent") {
		t.Error("expected false for canceling nonexistent agent")
	}
}

func TestManagerCancelPendingAgent(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 5})
	id := mgr.Spawn("pending", "task", "", nil, context.Background())
	if !mgr.Cancel(id) {
		t.Error("expected true for canceling pending agent")
	}
	sa, _ := mgr.Get(id)
	if sa.Status != StatusCancelled {
		t.Errorf("expected cancelled, got %q", sa.Status)
	}
}

func TestManagerCancelRunningAgent(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 5})
	id := mgr.Spawn("runner", "task", "", nil, context.Background())

	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.SetCancel(id, cancel)

	if !mgr.Cancel(id) {
		t.Error("expected true for canceling running agent")
	}

	sa, _ := mgr.Get(id)
	if sa.Status != StatusCancelled {
		t.Errorf("expected cancelled, got %q", sa.Status)
	}
	if sa.EndedAt.IsZero() {
		t.Error("EndedAt should be set after cancel")
	}
}

func TestManagerCancelAllEmpty(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{})
	if n := mgr.CancelAll(); n != 0 {
		t.Errorf("expected 0 cancelled, got %d", n)
	}
}

func TestManagerCancelAllMixed(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 5})

	id1 := mgr.Spawn("runner", "task", "", nil, context.Background())
	id2 := mgr.Spawn("pending", "task", "", nil, context.Background())

	_, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	mgr.SetCancel(id1, cancel1)

	cancelled := mgr.CancelAll()
	if cancelled != 2 {
		t.Errorf("expected 2 cancelled, got %d", cancelled)
	}

	sa1, _ := mgr.Get(id1)
	if sa1.Status != StatusCancelled {
		t.Errorf("expected running agent to be cancelled, got %q", sa1.Status)
	}

	sa2, _ := mgr.Get(id2)
	if sa2.Status != StatusCancelled {
		t.Errorf("expected pending agent to be cancelled, got %q", sa2.Status)
	}
}

func TestManagerSetCancelDoesNotResurrectCancelledAgent(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 5})
	id := mgr.Spawn("pending", "task", "", nil, context.Background())
	if !mgr.Cancel(id) {
		t.Fatal("expected cancel to succeed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if mgr.SetCancel(id, cancel) {
		t.Fatal("expected SetCancel to refuse cancelled agent")
	}
	sa, _ := mgr.Get(id)
	if sa.Status != StatusCancelled {
		t.Fatalf("expected cancelled agent to remain cancelled, got %q", sa.Status)
	}
	if ctx.Err() != context.Canceled {
		t.Fatalf("expected SetCancel to cancel the provided context, got %v", ctx.Err())
	}
}

func TestManagerCompleteSuccess(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 5})
	id := mgr.Spawn("comp", "task", "", nil, context.Background())

	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.SetCancel(id, cancel)

	mgr.Complete(id, "result text", nil)

	sa, _ := mgr.Get(id)
	if sa.Status != StatusCompleted {
		t.Errorf("expected completed, got %q", sa.Status)
	}
	if sa.Result != "result text" {
		t.Errorf("Result = %q, want 'result text'", sa.Result)
	}
	if sa.CurrentPhase != "completed" {
		t.Errorf("CurrentPhase = %q, want 'completed'", sa.CurrentPhase)
	}
	if sa.EndedAt.IsZero() {
		t.Error("EndedAt should be set")
	}
}

func TestManagerCompleteError(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 5})
	id := mgr.Spawn("fail", "task", "", nil, context.Background())

	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.SetCancel(id, cancel)

	mgr.Complete(id, "partial", context.DeadlineExceeded)

	sa, _ := mgr.Get(id)
	if sa.Status != StatusFailed {
		t.Errorf("expected failed, got %q", sa.Status)
	}
	if sa.Result != "partial" {
		t.Errorf("Result = %q, want 'partial'", sa.Result)
	}
	if sa.Error == nil {
		t.Error("Error should not be nil")
	}
	if sa.CurrentPhase != "failed" {
		t.Errorf("CurrentPhase = %q, want 'failed'", sa.CurrentPhase)
	}
}

func TestManagerCompleteIdempotent(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 5})
	id := mgr.Spawn("idem", "task", "", nil, context.Background())

	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.SetCancel(id, cancel)

	mgr.Complete(id, "first result", nil)
	mgr.Complete(id, "second result", nil)

	sa, _ := mgr.Get(id)
	if sa.Result != "first result" {
		t.Errorf("expected first result preserved, got %q", sa.Result)
	}
}

func TestManagerCompleteAfterCancel(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 5})
	id := mgr.Spawn("canc", "task", "", nil, context.Background())

	_, cancel := context.WithCancel(context.Background())
	mgr.SetCancel(id, cancel)

	mgr.Cancel(id)

	mgr.Complete(id, "too late", nil)

	sa, _ := mgr.Get(id)
	if sa.Status != StatusCancelled {
		t.Errorf("expected cancelled, got %q", sa.Status)
	}
	if sa.Result != "too late" {
		t.Errorf("expected backfilled result %q, got %q", "too late", sa.Result)
	}
	if sa.Error != context.Canceled {
		t.Errorf("expected error to stay context.Canceled, got %v", sa.Error)
	}
	cancel()
}

func TestManagerCompleteNonexistent(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{})
	mgr.Complete("nonexistent", "result", nil)
}

func TestManagerGetTaskOutputNonexistent(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{})
	_, ok := mgr.GetTaskOutput("nonexistent")
	if ok {
		t.Error("expected false for nonexistent agent output")
	}
}

func TestManagerGetTaskOutputCompleted(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 5})
	id := mgr.Spawn("out", "task", "", nil, context.Background())

	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.SetCancel(id, cancel)
	mgr.Complete(id, "the result", nil)

	output, ok := mgr.GetTaskOutput(id)
	if !ok {
		t.Error("expected true")
	}
	if output != "the result" {
		t.Errorf("output = %q, want 'the result'", output)
	}
}

func TestManagerGetTaskOutputRunningWithProgress(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 5})
	id := mgr.Spawn("prog", "task", "", nil, context.Background())

	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.SetCancel(id, cancel)
	mgr.UpdateProgress(id, "half done")

	output, ok := mgr.GetTaskOutput(id)
	if !ok {
		t.Error("expected true")
	}
	if output != "[in progress] half done" {
		t.Errorf("output = %q, want '[in progress] half done'", output)
	}
}

func TestManagerGetTaskOutputRunningNoProgress(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 5})
	id := mgr.Spawn("nopro", "task", "", nil, context.Background())

	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.SetCancel(id, cancel)

	output, ok := mgr.GetTaskOutput(id)
	if !ok {
		t.Error("expected true")
	}
	if output != "[running, no output yet]" {
		t.Errorf("output = %q", output)
	}
}

func TestManagerGetTaskOutputPendingNoProgress(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 5})
	id := mgr.Spawn("pend", "task", "", nil, context.Background())

	output, ok := mgr.GetTaskOutput(id)
	if !ok {
		t.Error("expected true")
	}

	if output != "[pending, not started yet]" {
		t.Errorf("expected pending marker, got %q", output)
	}
}

func TestManagerGetTaskOutputFailed(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 5})
	sa := &SubAgent{ID: "sa-1", Task: "t"}
	mgr.mu.Lock()
	mgr.agents["sa-1"] = sa
	mgr.mu.Unlock()

	sa.mu.Lock()
	sa.Status = StatusFailed
	sa.Error = errors.New("context deadline exceeded")
	sa.mu.Unlock()

	output, ok := mgr.GetTaskOutput("sa-1")
	if !ok {
		t.Fatal("expected true")
	}
	if output != "[failed] context deadline exceeded" {
		t.Fatalf("expected failed marker with error, got %q", output)
	}

	sa.mu.Lock()
	sa.Status = StatusCancelled
	sa.Error = errors.New("context canceled")
	sa.mu.Unlock()
	if output, _ := mgr.GetTaskOutput("sa-1"); output != "[failed] context canceled" {
		t.Fatalf("expected cancelled error surfaced, got %q", output)
	}
}

func TestManagerSemaphoreAcquireRelease(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 2})

	if err := mgr.AcquireSemaphore(context.Background()); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if err := mgr.AcquireSemaphore(context.Background()); err != nil {
		t.Fatalf("second acquire: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := mgr.AcquireSemaphore(ctx)
	if err == nil {
		t.Error("expected timeout on third acquire")
	}

	mgr.ReleaseSemaphore()
	if err := mgr.AcquireSemaphore(context.Background()); err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	mgr.ReleaseSemaphore()
	mgr.ReleaseSemaphore()
}

func TestManagerSemaphoreCancelledCtx(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 1})
	mgr.AcquireSemaphore(context.Background())

	ctx2, cancel := context.WithCancel(context.Background())
	cancel()

	err := mgr.AcquireSemaphore(ctx2)
	if err == nil {
		t.Error("expected error from cancelled context")
	}
	mgr.ReleaseSemaphore()
}

func TestManagerDefaults(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{})
	if mgr.Timeout() != 180*time.Minute {
		t.Errorf("default timeout = %v, want 180m", mgr.Timeout())
	}
	if mgr.ShowOutput() {
		t.Error("default ShowOutput should be false")
	}
}

func TestManagerCustomTimeout(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{Timeout: 5 * time.Minute})
	if mgr.Timeout() != 5*time.Minute {
		t.Errorf("timeout = %v, want 5m", mgr.Timeout())
	}
}

func TestManagerShowOutput(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{ShowOutput: true})
	if !mgr.ShowOutput() {
		t.Error("ShowOutput should be true")
	}
}

func TestManagerRootContextAndShutdown(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{})
	ctx := mgr.RootContext()
	if ctx == nil {
		t.Fatal("RootContext should not be nil")
	}
	if err := ctx.Err(); err != nil {
		t.Errorf("expected no error before shutdown, got %v", err)
	}

	mgr.Shutdown()
	if mgr.RootContext().Err() == nil {
		t.Error("expected error after shutdown")
	}
}

func TestManagerShutdown_DoubleCall(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{})
	mgr.Shutdown()

	mgr.Shutdown()
}

func TestManagerSetOnComplete(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 5})

	var completedID string
	var mu sync.Mutex
	mgr.SetOnComplete(func(sa *SubAgent) {
		mu.Lock()
		completedID = sa.ID
		mu.Unlock()
	})

	id := mgr.Spawn("cb", "task", "", nil, context.Background())
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.SetCancel(id, cancel)
	mgr.Complete(id, "done", nil)

	mu.Lock()
	defer mu.Unlock()
	if completedID != id {
		t.Errorf("onComplete got %q, want %q", completedID, id)
	}
}

func TestManagerSetOnUpdate(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 5})

	callCount := 0
	var mu sync.Mutex
	mgr.SetOnUpdate(func(sa *SubAgent) {
		mu.Lock()
		callCount++
		mu.Unlock()
	})

	id := mgr.Spawn("upd", "task", "", nil, context.Background())
	mgr.Notify(id)

	mu.Lock()
	defer mu.Unlock()
	if callCount != 1 {
		t.Errorf("expected 1 update call, got %d", callCount)
	}
}

func TestManagerUpdateProgress(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 5})
	id := mgr.Spawn("prog", "task", "", nil, context.Background())

	mgr.UpdateProgress(id, "50% done")

	snap, _ := mgr.Snapshot(id)
	if snap.ProgressSummary != "50% done" {
		t.Errorf("ProgressSummary = %q, want 50%% done", snap.ProgressSummary)
	}
}

func TestManagerUpdateProgressNonexistent(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{})
	mgr.UpdateProgress("nonexistent", "progress")
}

func TestManagerUpdateActivity(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 5})
	id := mgr.Spawn("act", "task", "", nil, context.Background())

	mgr.UpdateActivity(id, "running", "read_file", `{"path":"/tmp"}`)

	snap, _ := mgr.Snapshot(id)
	if snap.CurrentPhase != "running" {
		t.Errorf("CurrentPhase = %q", snap.CurrentPhase)
	}
	if snap.CurrentTool != "read_file" {
		t.Errorf("CurrentTool = %q", snap.CurrentTool)
	}
	if snap.CurrentArgs != `{"path":"/tmp"}` {
		t.Errorf("CurrentArgs = %q", snap.CurrentArgs)
	}
}

func TestManagerUpdateActivityNonexistent(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{})
	mgr.UpdateActivity("nonexistent", "x", "y", "z")
}

func TestManagerSendToAgent(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 5})
	id := mgr.Spawn("mail", "task", "", nil, context.Background())

	msg := AgentMessage{From: "parent", Message: "hello", Summary: "greeting"}
	if err := mgr.SendToAgent(id, msg); err != nil {
		t.Fatalf("SendToAgent error: %v", err)
	}

	sa, _ := mgr.Get(id)
	select {
	case received := <-sa.Mailbox:
		if received.Message != "hello" {
			t.Errorf("received message = %q, want 'hello'", received.Message)
		}
	default:
		t.Error("expected message in mailbox")
	}
}

func TestManagerSendToAgentNonexistent(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{})
	err := mgr.SendToAgent("nonexistent", AgentMessage{})
	if err == nil {
		t.Error("expected error for nonexistent agent")
	}
}

func TestManagerSendToAgentFullMailbox(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 5})
	id := mgr.Spawn("full", "task", "", nil, context.Background())

	sa, _ := mgr.Get(id)
	for i := 0; i < 16; i++ {
		sa.Mailbox <- AgentMessage{Message: "fill"}
	}

	err := mgr.SendToAgent(id, AgentMessage{Message: "overflow"})
	if err == nil {
		t.Error("expected error for full mailbox")
	}
}

func TestManagerBroadcast(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 5})
	id1 := mgr.Spawn("r1", "task", "", nil, context.Background())
	id2 := mgr.Spawn("r2", "task", "", nil, context.Background())
	mgr.Spawn("p1", "task", "", nil, context.Background())

	_, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	mgr.SetCancel(id1, cancel1)
	_, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	mgr.SetCancel(id2, cancel2)

	sent := mgr.Broadcast(AgentMessage{Message: "broadcast"})
	if len(sent) != 2 {
		t.Errorf("expected 2 sent, got %d", len(sent))
	}
}

func TestManagerBroadcastEmpty(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{})
	sent := mgr.Broadcast(AgentMessage{})
	if len(sent) != 0 {
		t.Errorf("expected 0 sent, got %d", len(sent))
	}
}

func TestManagerNotifyStreamText(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 5})

	var receivedID, receivedText string
	var mu sync.Mutex
	mgr.SetOnStreamText(func(agentID, text string) {
		mu.Lock()
		receivedID = agentID
		receivedText = text
		mu.Unlock()
	})

	mgr.NotifyStreamText("sa-1", "hello chunk")

	mgr.FlushStreamBatch()

	mu.Lock()
	defer mu.Unlock()
	if receivedID != "sa-1" {
		t.Errorf("agentID = %q, want 'sa-1'", receivedID)
	}
	if receivedText != "hello chunk" {
		t.Errorf("text = %q, want 'hello chunk'", receivedText)
	}
}

func TestManagerNotifyStreamTextNoCallback(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{})
	mgr.NotifyStreamText("sa-1", "text")
}

func TestManagerNotifyToolCall(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 5})

	var receivedArgs []string
	var mu sync.Mutex
	mgr.SetOnToolCall(func(agentID, toolID, toolName, displayName, args, detail string) {
		mu.Lock()
		receivedArgs = []string{agentID, toolID, toolName, displayName, args, detail}
		mu.Unlock()
	})

	mgr.NotifyToolCall("sa-1", "tc-1", "read_file", "Read", `{"path":"/x"}`, "reading file")

	mu.Lock()
	defer mu.Unlock()
	if len(receivedArgs) != 6 || receivedArgs[0] != "sa-1" {
		t.Errorf("received args = %v", receivedArgs)
	}
}

func TestManagerNotifyToolResult(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 5})

	var receivedArgs []interface{}
	var mu sync.Mutex
	mgr.SetOnToolResult(func(agentID, toolID, toolName, displayName, detail, result string, isError bool) {
		mu.Lock()
		receivedArgs = []interface{}{agentID, toolID, toolName, displayName, detail, result, isError}
		mu.Unlock()
	})

	mgr.NotifyToolResult("sa-1", "tc-1", "read_file", "Read", "/x", "file contents", false)

	mu.Lock()
	defer mu.Unlock()
	if len(receivedArgs) != 7 {
		t.Errorf("received args = %v", receivedArgs)
	}
	if receivedArgs[6] != false {
		t.Error("expected isError=false")
	}
}

func TestSubAgentEventOverflow(t *testing.T) {
	sa := &SubAgent{
		ID:     "overflow",
		Status: StatusRunning,
	}

	for turn := 0; turn < 250; turn++ {
		sa.appendEvent(AgentEvent{Type: AgentEventToolCall, ToolName: "read_file", ToolID: fmt.Sprintf("call-%d", turn)})
		sa.appendEvent(AgentEvent{Type: AgentEventToolResult, ToolName: "read_file", ToolID: fmt.Sprintf("call-%d", turn)})
	}

	events := sa.Events()

	if len(events) > maxAgentEvents {
		t.Errorf("expected at most %d events after overflow, got %d", maxAgentEvents, len(events))
	}

	snap := sa.snapshot()
	if snap.EventsDropped < 6 {
		t.Errorf("expected at least 6 dropped (1 turn), got %d", snap.EventsDropped)
	}

	if len(events) > 0 && events[0].Type == AgentEventText {
		t.Errorf("first event should be at a turn boundary, not a text fragment")
	}
}

func TestSubAgentIncrementToolCalls(t *testing.T) {
	sa := &SubAgent{ID: "tc", Status: StatusRunning}
	if sa.ToolCallCount != 0 {
		t.Errorf("expected 0 tool calls initially")
	}
	sa.IncrementToolCalls()
	sa.IncrementToolCalls()
	if sa.ToolCallCount != 2 {
		t.Errorf("expected 2 tool calls, got %d", sa.ToolCallCount)
	}
}

func TestSubAgentSnapshotError(t *testing.T) {
	sa := &SubAgent{
		ID:     "err",
		Status: StatusFailed,
		Error:  context.DeadlineExceeded,
	}
	snap := sa.snapshot()
	if snap.Error != "context deadline exceeded" {
		t.Errorf("snapshot error = %q", snap.Error)
	}
}

func TestSubAgentSnapshotNilError(t *testing.T) {
	sa := &SubAgent{ID: "ok", Status: StatusCompleted}
	snap := sa.snapshot()
	if snap.Error != "" {
		t.Errorf("expected empty error, got %q", snap.Error)
	}
}

func TestCompactProgressSummary(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{
			name:   "with all fields",
			input:  "Job ID: abc-123\nSome noise\nStatus: running\nMore noise\nTotal lines: 42\n",
			expect: "Job ID: abc-123 • Status: running • Total lines: 42",
		},
		{
			name:   "only job id",
			input:  "Job ID: xyz\nrandom output",
			expect: "Job ID: xyz",
		},
		{
			name:   "no matching lines",
			input:  "random output\nno progress info\n",
			expect: "",
		},
		{
			name:   "empty input",
			input:  "",
			expect: "",
		},
		{
			name:   "status and total",
			input:  "Status: completed\nTotal lines: 100",
			expect: "Status: completed • Total lines: 100",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compactProgressSummary(tt.input)
			if got != tt.expect {
				t.Errorf("compactProgressSummary(%q) = %q, want %q", tt.input, got, tt.expect)
			}
		})
	}
}

func TestSubagentToolProgressSummary(t *testing.T) {

	result := "Job ID: abc\nStatus: running"
	summary := subagentToolProgressSummary("start_command", result)
	if summary == "" {
		t.Error("expected non-empty summary for start_command")
	}

	summary = subagentToolProgressSummary("read_file", "file contents")
	if summary != "" {
		t.Errorf("expected empty summary for read_file, got %q", summary)
	}
}

func TestWaitNotFound(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{})
	_, err := Wait(context.Background(), mgr, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent agent")
	}
}

func TestWaitCompleted(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 5})
	id := mgr.Spawn("wait", "task", "", nil, context.Background())

	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.SetCancel(id, cancel)
	mgr.Complete(id, "done", nil)

	result, err := Wait(context.Background(), mgr, id)
	if err != nil {
		t.Fatalf("Wait error: %v", err)
	}
	if result != "done" {
		t.Errorf("result = %q, want 'done'", result)
	}
}

func TestWaitFailed(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 5})
	id := mgr.Spawn("fail", "task", "", nil, context.Background())

	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.SetCancel(id, cancel)
	mgr.Complete(id, "partial", context.DeadlineExceeded)

	result, err := Wait(context.Background(), mgr, id)
	if err == nil {
		t.Error("expected error for failed agent")
	}
	if result != "partial" {
		t.Errorf("result = %q, want 'partial'", result)
	}
}

func TestWaitForSnapshotNotFound(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{})
	_, err := WaitForSnapshot(context.Background(), mgr, "nonexistent", 0)
	if err == nil {
		t.Error("expected error for nonexistent agent")
	}
}

func TestWaitForSnapshotImmediate(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 5})
	id := mgr.Spawn("snap", "task", "", nil, context.Background())

	snap, err := WaitForSnapshot(context.Background(), mgr, id, 0)
	if err != nil {
		t.Fatalf("WaitForSnapshot error: %v", err)
	}
	if snap.ID != id {
		t.Errorf("snap.ID = %q, want %q", snap.ID, id)
	}
}

func TestWaitForSnapshotCompletedBeforeTimeout(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 5})
	id := mgr.Spawn("snap", "task", "", nil, context.Background())

	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.SetCancel(id, cancel)

	go func() {
		time.Sleep(50 * time.Millisecond)
		mgr.Complete(id, "done", nil)
	}()

	snap, err := WaitForSnapshot(context.Background(), mgr, id, 2*time.Second)
	if err != nil {
		t.Fatalf("WaitForSnapshot error: %v", err)
	}
	if snap.Status != StatusCompleted {
		t.Errorf("status = %q, want completed", snap.Status)
	}
}

func TestWaitForSnapshotTimeout(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 5})
	id := mgr.Spawn("slow", "task", "", nil, context.Background())

	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.SetCancel(id, cancel)

	snap, err := WaitForSnapshot(context.Background(), mgr, id, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForSnapshot error: %v", err)
	}
	if snap.Status != StatusRunning {
		t.Errorf("status = %q, want running (timeout returns current snapshot)", snap.Status)
	}
}

func TestWaitForSnapshotCtxCancelled(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 5})
	id := mgr.Spawn("ctx", "task", "", nil, context.Background())

	ctx2, cancel := context.WithCancel(context.Background())
	mgr.SetCancel(id, cancel)

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := WaitForSnapshot(ctx2, mgr, id, 2*time.Second)
	if err == nil {
		t.Error("expected error from cancelled context")
	}
}

func TestAgentEventTypeValues(t *testing.T) {
	if AgentEventText != 0 {
		t.Errorf("AgentEventText = %d, want 0", AgentEventText)
	}
	if AgentEventToolCall != 1 {
		t.Errorf("AgentEventToolCall = %d, want 1", AgentEventToolCall)
	}
	if AgentEventToolResult != 2 {
		t.Errorf("AgentEventToolResult = %d, want 2", AgentEventToolResult)
	}
	if AgentEventError != 3 {
		t.Errorf("AgentEventError = %d, want 3", AgentEventError)
	}
	if AgentEventReasoning != 4 {
		t.Errorf("AgentEventReasoning = %d, want 4", AgentEventReasoning)
	}
}

func TestSubAgentSetGetStatus(t *testing.T) {
	sa := &SubAgent{ID: "sgs", Status: StatusPending}
	sa.setStatus(StatusRunning)
	if sa.getStatus() != StatusRunning {
		t.Errorf("getStatus = %q, want running", sa.getStatus())
	}
}

func TestSubAgentSetActivity(t *testing.T) {
	sa := &SubAgent{ID: "act"}
	sa.setActivity("tool", "edit_file", "args")
	if sa.CurrentPhase != "tool" || sa.CurrentTool != "edit_file" || sa.CurrentArgs != "args" {
		t.Errorf("activity mismatch: phase=%q tool=%q args=%q", sa.CurrentPhase, sa.CurrentTool, sa.CurrentArgs)
	}
}

func TestSubAgentSetProgressSummary(t *testing.T) {
	sa := &SubAgent{ID: "ps"}
	sa.setProgressSummary("half done")
	if sa.ProgressSummary != "half done" {
		t.Errorf("ProgressSummary = %q", sa.ProgressSummary)
	}
}

func TestSubAgentSetStartedAt(t *testing.T) {
	sa := &SubAgent{ID: "sa"}
	now := time.Now()
	sa.setStartedAt(now)
	if !sa.StartedAt.Equal(now) {
		t.Errorf("StartedAt = %v, want %v", sa.StartedAt, now)
	}
}

func TestSubAgentRecordAndAppendEvent(t *testing.T) {
	sa := &SubAgent{ID: "re"}
	sa.RecordEvent(AgentEvent{Type: AgentEventText, Text: "recorded"})
	sa.AppendEvent(AgentEvent{Type: AgentEventToolCall, ToolName: "read_file"})
	events := sa.Events()
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Text != "recorded" {
		t.Errorf("event[0].Text = %q", events[0].Text)
	}
	if events[1].ToolName != "read_file" {
		t.Errorf("event[1].ToolName = %q", events[1].ToolName)
	}
}

func TestSubAgentEventsCopy(t *testing.T) {
	sa := &SubAgent{ID: "copy"}
	sa.AppendEvent(AgentEvent{Type: AgentEventText, Text: "original"})
	events := sa.Events()
	events[0].Text = "modified"
	original := sa.Events()
	if original[0].Text != "original" {
		t.Error("Events() should return a copy, not shared reference")
	}
}

func TestSubAgentSnapshotToolsCopy(t *testing.T) {
	sa := &SubAgent{
		ID:     "tools",
		Status: StatusRunning,
		Tools:  []string{"a", "b"},
	}
	snap := sa.snapshot()
	snap.Tools[0] = "modified"
	if sa.Tools[0] != "a" {
		t.Error("snapshot should copy Tools slice")
	}
}

func TestSubAgentSnapshotEventsCopy(t *testing.T) {
	sa := &SubAgent{
		ID:     "events",
		Status: StatusRunning,
	}
	sa.AppendEvent(AgentEvent{Type: AgentEventText, Text: "original"})
	snap := sa.snapshot()
	snap.Events[0].Text = "modified"
	original := sa.Events()
	if original[0].Text != "original" {
		t.Error("snapshot should copy Events slice")
	}
}

func TestManagerNotifyNonexistent(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{})
	mgr.Notify("nonexistent")
}

func TestNotifyUpdateThrottleDeliversDuringStreaming(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 5})

	var delivered int
	var mu sync.Mutex
	mgr.SetOnUpdate(func(sa *SubAgent) {
		mu.Lock()
		delivered++
		mu.Unlock()
	})

	id := mgr.Spawn("test", "task", "display", nil, context.Background())

	for i := 0; i < 20; i++ {
		mgr.Notify(id)
		time.Sleep(15 * time.Millisecond)
	}

	mu.Lock()
	count := delivered
	mu.Unlock()

	if count < 2 {
		t.Errorf("expected at least 2 deliveries during ~300ms streaming, got %d (throttle bug: lastNotify advanced on skipped calls)", count)
	}
}
