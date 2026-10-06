package agent

// #3436: r484 wired maybeToolflowSuggestion before every tool batch, but the
// one-shot gate only armed on a HIT - the (common) miss path re-ran the
// uncached multi-GB AnalyzeToolFlows scan per batch. The fix is a
// process-wide TTL cache around mining; these tests pin the amortization
// semantics.

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/memory"
)

// withToolflowMineStub replaces the mining hook for one test and restores the
// real one (plus cache state) afterwards.
func withToolflowMineStub(t *testing.T, stub func(dir string, max int) ([]memory.ToolFlowPattern, error)) {
	t.Helper()
	prev := toolflowMine
	toolflowMine = stub
	resetToolflowCacheForTest()
	t.Cleanup(func() {
		toolflowMine = prev
		resetToolflowCacheForTest()
	})
}

func TestIssue3436MiningHappensOncePerWindow(t *testing.T) {
	var calls int32
	pats := []memory.ToolFlowPattern{
		{Prefix: []string{"read_file", "edit_file"}, Next: "run_command", Count: 9, Confidence: 0.9},
	}
	withToolflowMineStub(t, func(dir string, max int) ([]memory.ToolFlowPattern, error) {
		atomic.AddInt32(&calls, 1)
		return pats, nil
	})
	a := &Agent{}
	// Miss, hit, miss across distinct agents (runs) inside the TTL window:
	// mining must execute exactly once.
	if a.maybeToolflowSuggestion([]string{"grep", "glob"}) != "" {
		t.Fatalf("non-matching sequence must not hint")
	}
	if got := a.maybeToolflowSuggestion([]string{"grep", "read_file", "edit_file"}); got == "" {
		t.Fatalf("matching suffix must hint")
	}
	b := &Agent{} // fresh run: one-shot gate re-armed, cache still valid
	if got := b.maybeToolflowSuggestion([]string{"grep", "read_file", "edit_file"}); got == "" {
		t.Fatalf("second run within window must still match cached patterns")
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("mining must run once per TTL window, ran %d times", n)
	}
}

func TestIssue3436ErrorArmsCacheNoRetry(t *testing.T) {
	var calls int32
	withToolflowMineStub(t, func(dir string, max int) ([]memory.ToolFlowPattern, error) {
		atomic.AddInt32(&calls, 1)
		return nil, errors.New("boom")
	})
	a := &Agent{}
	for i := 0; i < 3; i++ {
		if got := a.maybeToolflowSuggestion([]string{"read_file", "edit_file"}); got != "" {
			t.Fatalf("mining error must not hint, got %q", got)
		}
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("persistent mining error must not retry per batch, ran %d times", n)
	}
}

func TestIssue3436ExpiredWindowRemines(t *testing.T) {
	var calls int32
	withToolflowMineStub(t, func(dir string, max int) ([]memory.ToolFlowPattern, error) {
		atomic.AddInt32(&calls, 1)
		return nil, nil
	})
	a := &Agent{}
	a.maybeToolflowSuggestion([]string{"read_file", "edit_file"})
	// Force the cached entry to look older than the TTL.
	toolflowCacheMu.Lock()
	toolflowCacheAt = time.Now().Add(-toolflowCacheTTL - time.Second)
	toolflowCacheMu.Unlock()
	a.maybeToolflowSuggestion([]string{"read_file", "edit_file"})
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Errorf("expired window must re-mine exactly once more, ran %d times", n)
	}
}

func TestIssue3436RealStoreScannedOnceAcrossBatches(t *testing.T) {
	// End-to-end shape of the regression: a real (temp) store that yields no
	// matching pattern - the per-batch loop used to re-scan it every call.
	resetToolflowCacheForTest()
	defer resetToolflowCacheForTest()
	t.Setenv("HOME", t.TempDir())
	a := &Agent{}
	for i := 0; i < 5; i++ {
		if got := a.maybeToolflowSuggestion([]string{"tool_x", "tool_y", "tool_z"}); got != "" {
			t.Fatalf("cold store must not hint, got %q", got)
		}
	}
	toolflowCacheMu.Lock()
	armed, at := toolflowCacheReady, toolflowCacheAt
	toolflowCacheMu.Unlock()
	if !armed || at.IsZero() {
		t.Errorf("cold store must still arm the cache (no per-batch re-scan), armed=%v", armed)
	}
}
