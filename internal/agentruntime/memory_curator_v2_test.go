package agentruntime

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/provider"
)

// scriptedCuratorProvider returns queued responses in order (one per Chat
// call), so multi-pass settlement can be pinned deterministically.
type scriptedCuratorProvider struct {
	mu        sync.Mutex
	responses []string
	errs      []bool // parallel: true = fail this call
	calls     int
}

func (s *scriptedCuratorProvider) Name() string { return "scripted-curator" }

func (s *scriptedCuratorProvider) Chat(ctx context.Context, msgs []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.calls
	s.calls++
	if i < len(s.errs) && s.errs[i] {
		return nil, errors.New("scripted failure")
	}
	if i >= len(s.responses) {
		i = len(s.responses) - 1
	}
	return &provider.ChatResponse{Message: provider.Message{
		Role:    "assistant",
		Content: []provider.ContentBlock{provider.TextBlock(s.responses[i])},
	}}, nil
}

func (s *scriptedCuratorProvider) ChatStream(ctx context.Context, msgs []provider.Message, tools []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent)
	close(ch)
	return ch, nil
}

func (s *scriptedCuratorProvider) CountTokens(ctx context.Context, msgs []provider.Message) (int, error) {
	return 0, nil
}

func (s *scriptedCuratorProvider) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// v2 amendment pin: a batch larger than the per-pass action cap must settle
// across MULTIPLE passes (no fixed batch ceiling); keys without an action
// in a pass carry to the next one.
func TestMemoryCurator_LoopsUntilBatchSettled(t *testing.T) {
	withTestHome(t)
	project := memory.NewProjectAutoMemory(t.TempDir())
	for _, k := range []string{"k1", "k2", "k3"} {
		if err := project.SaveMemory(k, "one-time event; has completed"); err != nil {
			t.Fatal(err)
		}
	}
	del := func(keys ...string) string {
		acts := make([]string, 0, len(keys))
		for _, k := range keys {
			acts = append(acts, `{"op":"delete","scope":"project","key":"`+k+`","reason":"stale"}`)
		}
		return `{"actions":[` + strings.Join(acts, ",") + `]}`
	}
	// pass 1 settles k1+k2 (per-pass cap 2), pass 2 settles k3.
	scripted := &scriptedCuratorProvider{
		responses: []string{del("k1", "k2"), del("k3")},
	}
	cur := NewMemoryCurator(nil, project, scripted, MemoryCuratorOptions{MaxActions: 2})
	batch := []memory.ChangeEvent{
		{Scope: "project", Op: "save", Key: "k1"},
		{Scope: "project", Op: "save", Key: "k2"},
		{Scope: "project", Op: "save", Key: "k3"},
	}
	cur.curateBatchLoop(context.Background(), batch)
	for _, k := range []string{"k1", "k2", "k3"} {
		if v, _ := project.LoadKey(k); v != "" {
			t.Fatalf("key %s not settled across passes (calls=%d)", k, scripted.callCount())
		}
	}
	if scripted.callCount() != 2 {
		t.Fatalf("expected exactly 2 passes, got %d", scripted.callCount())
	}
}

// v2 amendment pin: the startup sweep walks the backlog in fixed-size
// chunks and reports more==true until the pass completes.
func TestMemoryCurator_SweepChunksBacklog(t *testing.T) {
	withTestHome(t)
	project := memory.NewProjectAutoMemory(t.TempDir())
	for i := 0; i < 45; i++ {
		k := strings.Repeat("k", 1) + time.Duration(i).String() + "-sweep"
		if err := project.SaveMemory(k, "body"); err != nil {
			t.Fatal(err)
		}
	}
	cur := NewMemoryCurator(nil, project, &fakeCuratorProvider{}, MemoryCuratorOptions{})
	chunk, more := cur.nextSweepChunk()
	if len(chunk) != memoryCuratorSweepChunk || !more {
		t.Fatalf("first chunk = %d keys, more=%v; want %d, true", len(chunk), more, memoryCuratorSweepChunk)
	}
	seen := map[string]bool{}
	for _, ev := range chunk {
		seen[ev.Key] = true
	}
	if len(seen) != len(chunk) {
		t.Fatal("duplicate keys inside a sweep chunk")
	}
	rest, more2 := cur.nextSweepChunk()
	if len(rest) != 5 || more2 {
		t.Fatalf("second chunk = %d keys, more=%v; want 5, false", len(rest), more2)
	}
	// A completed pass signals with an empty chunk first (caller sleeps),
	// then the NEXT call restarts from scratch (maintenance re-pass).
	if chunk3, more3 := cur.nextSweepChunk(); len(chunk3) != 0 || more3 {
		t.Fatalf("completion signal expected empty chunk: %d keys, more=%v", len(chunk3), more3)
	}
	chunk4, more4 := cur.nextSweepChunk()
	if len(chunk4) != memoryCuratorSweepChunk || !more4 {
		t.Fatalf("post-completion re-pass did not restart: %d keys, more=%v", len(chunk4), more4)
	}
}

// v2 amendment pin: three consecutive failed passes trip the breaker.
func TestMemoryCurator_BreakerAfterConsecutiveFailures(t *testing.T) {
	withTestHome(t)
	project := memory.NewProjectAutoMemory(t.TempDir())
	if err := project.SaveMemory("k1", "body"); err != nil {
		t.Fatal(err)
	}
	scripted := &scriptedCuratorProvider{errs: []bool{true, true, true, true}}
	cur := NewMemoryCurator(nil, project, scripted, MemoryCuratorOptions{})
	batch := []memory.ChangeEvent{{Scope: "project", Op: "save", Key: "k1"}}
	for i := 0; i < memoryCuratorBreakerAfter; i++ {
		cur.curateBatchLoop(context.Background(), batch)
	}
	if cur.failStreak != memoryCuratorBreakerAfter {
		t.Fatalf("failStreak = %d, want %d", cur.failStreak, memoryCuratorBreakerAfter)
	}
	if !time.Now().Before(cur.breakerUntil) {
		t.Fatal("breaker not tripped after the configured streak")
	}
}
