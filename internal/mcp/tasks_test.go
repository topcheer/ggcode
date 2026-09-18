package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestIsTaskEnvelope(t *testing.T) {
	yes := []string{
		`{"resultType":"task","taskId":"t1","status":"working"}`,
		`{"taskId":"t1","status":"completed","resultType":"task"}`,
	}
	for _, s := range yes {
		if !isTaskEnvelope(json.RawMessage(s)) {
			t.Errorf("expected task envelope: %s", s)
		}
	}
	no := []string{
		`{"content":[{"type":"text","text":"ok"}]}`,
		`{"resultType":"complete","content":[]}`,
		`{"taskId":"t1","status":"working"}`, // no discriminator
		`{}`,
		`not json`,
	}
	for _, s := range no {
		if isTaskEnvelope(json.RawMessage(s)) {
			t.Errorf("unexpected task envelope: %s", s)
		}
	}
}

func TestTaskPollIntervalClamp(t *testing.T) {
	cases := []struct {
		hint int64
		want time.Duration
	}{
		{0, taskPollDefault},
		{50, taskPollMin},
		{2500, 2500 * time.Millisecond},
		{60000, taskPollMax},
		{-100, taskPollDefault},
	}
	var task Task
	for _, c := range cases {
		task.PollInterval = c.hint
		if got := task.pollInterval(); got != c.want {
			t.Errorf("hint=%d: got %v want %v", c.hint, got, c.want)
		}
	}
}

func TestAwaitTaskLoopWorkingThenCompleted(t *testing.T) {
	c := NewClient("test", "echo", nil)
	c.taskPollOverride = time.Millisecond

	var polls []Task
	get := func(ctx context.Context, id string) (Task, error) {
		polls = append(polls, Task{TaskID: id, Status: TaskStatusWorking})
		if len(polls) >= 2 {
			return Task{TaskID: id, Status: TaskStatusCompleted}, nil
		}
		return Task{TaskID: id, Status: TaskStatusWorking, PollInterval: 5}, nil
	}
	task, err := c.awaitTaskLoop(context.Background(), Task{TaskID: "t1", Status: TaskStatusWorking}, "t1", get)
	if err != nil {
		t.Fatalf("awaitTaskLoop: %v", err)
	}
	if task.Status != TaskStatusCompleted || task.TaskID != "t1" {
		t.Fatalf("unexpected terminal task: %+v", task)
	}
	if len(polls) != 2 {
		t.Fatalf("expected 2 polls, got %d", len(polls))
	}
}

func TestAwaitTaskLoopSeedTerminalSkipsPolling(t *testing.T) {
	c := NewClient("test", "echo", nil)
	get := func(ctx context.Context, id string) (Task, error) {
		t.Fatal("must not poll a seed-terminal task")
		return Task{}, nil
	}
	task, err := c.awaitTaskLoop(context.Background(), Task{TaskID: "t1", Status: TaskStatusCompleted}, "t1", get)
	if err != nil || task.Status != TaskStatusCompleted {
		t.Fatalf("seed completed: task=%+v err=%v", task, err)
	}
}

func TestAwaitTaskLoopInputRequired(t *testing.T) {
	c := NewClient("test", "echo", nil)
	c.taskPollOverride = time.Millisecond
	get := func(ctx context.Context, id string) (Task, error) {
		return Task{TaskID: id, Status: TaskStatusInputRequired, StatusMessage: "need ask_user"}, nil
	}
	_, err := c.awaitTaskLoop(context.Background(), Task{Status: TaskStatusWorking}, "t1", get)
	if err == nil || !strings.Contains(err.Error(), "input_required") {
		t.Fatalf("expected input_required error, got %v", err)
	}
}

func TestAwaitTaskLoopUnknownStatus(t *testing.T) {
	c := NewClient("test", "echo", nil)
	get := func(ctx context.Context, id string) (Task, error) {
		return Task{TaskID: id, Status: "exploded"}, nil
	}
	_, err := c.awaitTaskLoop(context.Background(), Task{Status: TaskStatusWorking}, "t1", get)
	if err == nil || !strings.Contains(err.Error(), "unknown status") {
		t.Fatalf("expected unknown-status error, got %v", err)
	}
}

func TestAwaitTaskLoopContextCancelled(t *testing.T) {
	c := NewClient("test", "echo", nil)
	c.taskPollOverride = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	get := func(ctx context.Context, id string) (Task, error) {
		cancel()
		return Task{TaskID: id, Status: TaskStatusWorking}, nil
	}
	_, err := c.awaitTaskLoop(ctx, Task{Status: TaskStatusWorking}, "t1", get)
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("expected cancellation error, got %v", err)
	}
}

func TestClientCapsDeclareTasks(t *testing.T) {
	c := NewClient("test", "echo", nil)
	caps := c.clientCaps()
	if caps.Tasks == nil {
		t.Fatal("client capability envelope must advertise tasks (SEP-1686)")
	}
	// The same declaration must survive the modern _meta envelope round-trip.
	meta := c.modernRequestMeta(ProtocolVersion20260728)
	raw, err := json.Marshal(meta[MetaKeyClientCapabilities])
	if err != nil {
		t.Fatalf("marshal caps: %v", err)
	}
	if !strings.Contains(string(raw), `"tasks"`) {
		t.Fatalf("modern envelope caps missing tasks: %s", raw)
	}
}
