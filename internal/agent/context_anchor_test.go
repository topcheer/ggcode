package agent

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestAnchorState_Reset(t *testing.T) {
	a := newAnchorState()
	a.fired = 2
	a.lastFireIter = 10
	a.userTask = "test task"
	a.taskExtracted = true

	a.reset()

	if a.fired != 0 || a.lastFireIter != 0 || a.userTask != "" || a.taskExtracted {
		t.Fatalf("reset did not clear state: %+v", a)
	}
}

func TestCheckAnchorReinforcement_EarlyIteration(t *testing.T) {
	a := newAnchorState()
	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Fix the bug"}}},
	}
	// iter 3 < minIter(5): should not fire
	result := a.checkAnchorReinforcement(3, 50000, 128000, msgs)
	if result != "" {
		t.Fatalf("expected empty result for early iteration, got: %s", result)
	}
}

func TestCheckAnchorReinforcement_LowUsage(t *testing.T) {
	a := newAnchorState()
	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Fix the bug"}}},
	}
	// iter 6, usage = 10% < 35%: should not fire
	result := a.checkAnchorReinforcement(6, 10000, 128000, msgs)
	if result != "" {
		t.Fatalf("expected empty result for low usage, got: %s", result)
	}
}

func TestCheckAnchorReinforcement_Fires(t *testing.T) {
	a := newAnchorState()
	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Implement the feature"}}},
	}
	// iter 6, usage = 50% > 35%: should fire
	result := a.checkAnchorReinforcement(6, 64000, 128000, msgs)
	if result == "" {
		t.Fatal("expected non-empty result for high usage + sufficient iteration")
	}
	if !strings.Contains(result, "[Context Anchor]") {
		t.Fatalf("result should contain [Context Anchor] tag, got: %s", result)
	}
	if !strings.Contains(result, "Implement the feature") {
		t.Fatalf("result should contain original task text, got: %s", result)
	}
	if a.fired != 1 {
		t.Fatalf("expected fired=1, got %d", a.fired)
	}
	if a.lastFireIter != 6 {
		t.Fatalf("expected lastFireIter=6, got %d", a.lastFireIter)
	}
}

func TestCheckAnchorReinforcement_MaxFires(t *testing.T) {
	a := newAnchorState()
	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Do something"}}},
	}
	// Fire twice (maxAnchorWarnings=2)
	a.checkAnchorReinforcement(6, 64000, 128000, msgs)
	a.checkAnchorReinforcement(11, 80000, 128000, msgs)

	// Third fire should not happen
	result := a.checkAnchorReinforcement(16, 100000, 128000, msgs)
	if result != "" {
		t.Fatalf("expected empty result after max fires, got: %s", result)
	}
	if a.fired != 2 {
		t.Fatalf("expected fired=2, got %d", a.fired)
	}
}

func TestCheckAnchorReinforcement_IterGap(t *testing.T) {
	a := newAnchorState()
	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Do something"}}},
	}
	// Fire at iter 6
	r1 := a.checkAnchorReinforcement(6, 64000, 128000, msgs)
	if r1 == "" {
		t.Fatal("expected first fire")
	}

	// Try at iter 8 (gap=2 < minGap=4): should not fire
	r2 := a.checkAnchorReinforcement(8, 70000, 128000, msgs)
	if r2 != "" {
		t.Fatalf("expected empty result due to iter gap, got: %s", r2)
	}

	// Try at iter 11 (gap=5 >= minGap=4): should fire
	r3 := a.checkAnchorReinforcement(11, 80000, 128000, msgs)
	if r3 == "" {
		t.Fatal("expected second fire after gap")
	}
}

func TestCheckAnchorReinforcement_ZeroContextWindow(t *testing.T) {
	a := newAnchorState()
	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Task"}}},
	}
	// contextWindow=0: should not fire (division-by-zero guard)
	result := a.checkAnchorReinforcement(10, 50000, 0, msgs)
	if result != "" {
		t.Fatalf("expected empty result for zero context window, got: %s", result)
	}
}

func TestExtractUserTask_SkipsBracketMessages(t *testing.T) {
	a := newAnchorState()
	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "[Tool Result] some output"}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Real user task"}}},
	}
	task := a.extractUserTask(msgs)
	if task != "Real user task" {
		t.Fatalf("expected 'Real user task', got '%s'", task)
	}
}

func TestExtractUserTask_Truncates(t *testing.T) {
	a := newAnchorState()
	longTask := strings.Repeat("x", 600)
	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: longTask}}},
	}
	task := a.extractUserTask(msgs)
	if len(task) > 500 {
		t.Fatalf("expected task truncated to <=500 chars, got %d", len(task))
	}
	if !strings.HasSuffix(task, "...") {
		t.Fatal("expected truncated task to end with ...")
	}
}

func TestExtractUserTask_CachesResult(t *testing.T) {
	a := newAnchorState()
	msgs1 := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "First task"}}},
	}
	task1 := a.extractUserTask(msgs1)

	// Different messages should return cached result
	msgs2 := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Second task"}}},
	}
	task2 := a.extractUserTask(msgs2)

	if task1 != task2 {
		t.Fatalf("expected cached result, got task1='%s' task2='%s'", task1, task2)
	}
}

func TestExtractUserTask_NoUserMessage(t *testing.T) {
	a := newAnchorState()
	msgs := []provider.Message{
		{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "system prompt"}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "response"}}},
	}
	task := a.extractUserTask(msgs)
	if task != "" {
		t.Fatalf("expected empty task for no user messages, got '%s'", task)
	}
}

func TestCheckAnchorReinforcement_NoUserTask(t *testing.T) {
	a := newAnchorState()
	msgs := []provider.Message{
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "response"}}},
	}
	// No user task to anchor: should not fire even with high usage
	result := a.checkAnchorReinforcement(10, 64000, 128000, msgs)
	if result != "" {
		t.Fatalf("expected empty result when no user task, got: %s", result)
	}
}

func TestTruncateTaskForDisplay(t *testing.T) {
	if truncateTaskForDisplay("short", 10) != "short" {
		t.Fatal("expected short string unchanged")
	}
	long := strings.Repeat("a", 100)
	result := truncateTaskForDisplay(long, 10)
	if len(result) != 10 {
		t.Fatalf("expected length 10, got %d", len(result))
	}
	if !strings.HasSuffix(result, "...") {
		t.Fatal("expected ellipsis suffix")
	}
}
