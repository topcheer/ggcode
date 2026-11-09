package agentruntime

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/provider"
)

// fakeCuratorProvider is a canned provider for curator tests: it returns a
// fixed response or a fixed error and counts Chat calls.
type fakeCuratorProvider struct {
	mu       sync.Mutex
	response string
	err      error
	calls    int
}

func (f *fakeCuratorProvider) Name() string { return "fake-curator" }

func (f *fakeCuratorProvider) Chat(ctx context.Context, msgs []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return &provider.ChatResponse{
		Message: provider.Message{
			Role:    "assistant",
			Content: []provider.ContentBlock{provider.TextBlock(f.response)},
		},
	}, nil
}

func (f *fakeCuratorProvider) ChatStream(ctx context.Context, msgs []provider.Message, tools []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent)
	close(ch)
	return ch, nil
}

func (f *fakeCuratorProvider) CountTokens(ctx context.Context, msgs []provider.Message) (int, error) {
	return 0, nil
}

func (f *fakeCuratorProvider) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// curatorWaitFor polls cond until it holds or the timeout expires.
func curatorWaitFor(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return cond()
}

func TestMemoryCurator_AppliesValidActions(t *testing.T) {
	withTestHome(t)
	global := memory.NewAutoMemory()
	project := memory.NewProjectAutoMemory(t.TempDir())
	if err := project.SaveMemory("build-process", "Use make verify-ci before pushing"); err != nil {
		t.Fatal(err)
	}

	fake := &fakeCuratorProvider{response: `{"actions":[` +
		`{"op":"save","scope":"project","key":"build-process","content":"Tags: build\nUse make verify-ci before pushing","reason":"add tags line"},` +
		`{"op":"delete","scope":"project","key":"stale-release-note","reason":"one-time completed event"}]}`}
	cur := NewMemoryCurator(global, project, fake, MemoryCuratorOptions{Debounce: 30 * time.Millisecond, DisableSweep: true})
	cur.Start(context.Background())
	defer cur.Stop()

	// The trigger mutation feeds the stale entry into the batch.
	if err := project.SaveMemory("stale-release-note", "Release 1.2.3 has completed; do NOT re-tag it"); err != nil {
		t.Fatal(err)
	}

	if !curatorWaitFor(t, 5*time.Second, func() bool {
		deleted, _ := project.LoadKey("stale-release-note")
		merged, _ := project.LoadKey("build-process")
		return deleted == "" && strings.Contains(merged, "Tags: build")
	}) {
		deleted, _ := project.LoadKey("stale-release-note")
		merged, _ := project.LoadKey("build-process")
		t.Fatalf("curator actions not applied: stale=%q merged=%q (provider calls: %d)", deleted, merged, fake.callCount())
	}
}

func TestMemoryCurator_BatchesEventsIntoOneCall(t *testing.T) {
	withTestHome(t)
	project := memory.NewProjectAutoMemory(t.TempDir())
	fake := &fakeCuratorProvider{response: `{"actions":[]}`}
	cur := NewMemoryCurator(nil, project, fake, MemoryCuratorOptions{Debounce: 150 * time.Millisecond, DisableSweep: true})
	cur.Start(context.Background())
	defer cur.Stop()

	for _, key := range []string{"build-a", "build-b", "build-c"} {
		if err := project.SaveMemory(key, "content"); err != nil {
			t.Fatal(err)
		}
	}

	if !curatorWaitFor(t, 5*time.Second, func() bool { return fake.callCount() >= 1 }) {
		t.Fatal("no curation batch ran")
	}
	// The three saves happened well inside one debounce window: exactly one
	// batch (and no further batches) must run.
	time.Sleep(3 * 150 * time.Millisecond)
	if got := fake.callCount(); got != 1 {
		t.Fatalf("expected exactly 1 LLM call for a 3-event burst, got %d", got)
	}
}

func TestMemoryCurator_FailingProviderAbandonsBatchHarmlessly(t *testing.T) {
	withTestHome(t)
	project := memory.NewProjectAutoMemory(t.TempDir())
	if err := project.SaveMemory("build-process", "Use make verify-ci"); err != nil {
		t.Fatal(err)
	}
	fake := &fakeCuratorProvider{err: errors.New("llm down")}
	cur := NewMemoryCurator(nil, project, fake, MemoryCuratorOptions{Debounce: 30 * time.Millisecond, DisableSweep: true})
	cur.Start(context.Background())
	defer cur.Stop()

	if err := project.SaveMemory("build-process", "Use make verify-ci and lint"); err != nil {
		t.Fatal(err)
	}
	if !curatorWaitFor(t, 5*time.Second, func() bool { return fake.callCount() >= 1 }) {
		t.Fatal("failed batch was never attempted")
	}
	// Entries untouched, no panic.
	content, _ := project.LoadKey("build-process")
	if !strings.Contains(content, "lint") {
		t.Fatalf("entry unexpectedly modified after failed batch: %q", content)
	}
	// Loop stays alive: a new event produces exactly one new attempt, no
	// retry storm for the abandoned batch.
	if err := project.SaveMemory("build-process", "third revision"); err != nil {
		t.Fatal(err)
	}
	if !curatorWaitFor(t, 5*time.Second, func() bool { return fake.callCount() >= 2 }) {
		t.Fatal("curator loop died after a failed batch")
	}
	if got := fake.callCount(); got != 2 {
		t.Fatalf("expected 2 attempts total (no retries), got %d", got)
	}
}

func TestMemoryCurator_IgnoresOwnEvents(t *testing.T) {
	withTestHome(t)
	project := memory.NewProjectAutoMemory(t.TempDir())
	if err := project.SaveMemory("build-process", "Use make verify-ci"); err != nil {
		t.Fatal(err)
	}
	fake := &fakeCuratorProvider{response: `{"actions":[{"op":"save","scope":"project","key":"build-process","content":"Tags: build\nUse make verify-ci"}]}`}
	cur := NewMemoryCurator(nil, project, fake, MemoryCuratorOptions{Debounce: 30 * time.Millisecond, DisableSweep: true})
	cur.Start(context.Background())
	defer cur.Stop()

	if err := project.SaveMemory("build-process", "Use make verify-ci v2"); err != nil {
		t.Fatal(err)
	}
	if !curatorWaitFor(t, 5*time.Second, func() bool {
		content, _ := project.LoadKey("build-process")
		return strings.Contains(content, "Tags: build")
	}) {
		t.Fatal("curator write never applied")
	}
	// The curator's own save must not feed back into another batch.
	time.Sleep(4 * 30 * time.Millisecond)
	if got := fake.callCount(); got != 1 {
		t.Fatalf("curator reacted to its own writes: %d LLM calls, want 1", got)
	}
}

func TestMemoryCurator_MaxActionsCap(t *testing.T) {
	withTestHome(t)
	project := memory.NewProjectAutoMemory(t.TempDir())
	for _, key := range []string{"stale-a", "stale-b"} {
		if err := project.SaveMemory(key, "Release has completed"); err != nil {
			t.Fatal(err)
		}
	}
	fake := &fakeCuratorProvider{response: `{"actions":[` +
		`{"op":"delete","scope":"project","key":"stale-a"},` +
		`{"op":"delete","scope":"project","key":"stale-b"}]}`}
	cur := NewMemoryCurator(nil, project, fake, MemoryCuratorOptions{Debounce: 30 * time.Millisecond, MaxActions: 1})
	cur.Start(context.Background())
	defer cur.Stop()

	if err := project.SaveMemory("stale-a", "Release 1 has completed"); err != nil {
		t.Fatal(err)
	}
	if !curatorWaitFor(t, 5*time.Second, func() bool {
		a, _ := project.LoadKey("stale-a")
		return a == ""
	}) {
		t.Fatal("first capped action never applied")
	}
	// Second action exceeded the cap of 1 and must remain live.
	b, _ := project.LoadKey("stale-b")
	if b == "" {
		t.Fatal("action beyond MaxActions was executed")
	}
}

func TestMemoryCurator_StartStopLifecycle(t *testing.T) {
	withTestHome(t)
	project := memory.NewProjectAutoMemory(t.TempDir())
	var calls atomic.Int32
	fake := &fakeCuratorProvider{response: `{"actions":[]}`}
	cur := NewMemoryCurator(nil, project, fake, MemoryCuratorOptions{Debounce: 10 * time.Millisecond})

	// Stop before Start is a no-op; double Start/Stop are idempotent.
	cur.Stop()
	cur.Start(context.Background())
	cur.Start(context.Background())
	cur.Stop()
	cur.Stop()

	// After Stop the sinks are unregistered: saves emit no batch.
	if err := project.SaveMemory("post-stop", "content"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if got := fake.callCount(); got != 0 {
		t.Fatalf("stopped curator still ran %d batch(es)", got)
	}
	if calls.Load() != 0 {
		t.Fatal("unexpected calls")
	}
}

func TestMemoryCurator_NilProviderNeverStarts(t *testing.T) {
	withTestHome(t)
	project := memory.NewProjectAutoMemory(t.TempDir())
	cur := NewMemoryCurator(nil, project, nil, MemoryCuratorOptions{Debounce: 10 * time.Millisecond})
	cur.Start(context.Background())
	if err := project.SaveMemory("x", "y"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	cur.Stop()
}

func TestMemoryCurator_ConfigGateDefaults(t *testing.T) {
	var nilCfg *config.Config
	if !nilCfg.MemoryCuratorEnabled() {
		t.Fatal("nil config must default the curator to enabled")
	}
	var cfg config.Config
	if !cfg.MemoryCuratorEnabled() {
		t.Fatal("zero-value config must enable the curator")
	}
	off := false
	cfg.Memory.Curator.Enabled = &off
	if cfg.MemoryCuratorEnabled() {
		t.Fatal("explicit enabled=false must disable the curator")
	}
	on := true
	cfg.Memory.Curator.Enabled = &on
	if !cfg.MemoryCuratorEnabled() {
		t.Fatal("explicit enabled=true must enable the curator")
	}
}
