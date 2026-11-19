package agent

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/provider"
)

// recordingCM5807 records the order of compaction primitives on the PTL
// retry path. Only the methods tryReactiveCompact touches are overridden.
// Kept free of #5807-only APIs so the file compiles standalone against the
// pre-fix parent (behavioral red-at-parent proof).
type recordingCM5807 struct {
	ctxpkg.ContextManager
	mu        sync.Mutex
	calls     []string
	tokens    int
	summarErr error
}

func (r *recordingCM5807) record(call string) {
	r.mu.Lock()
	r.calls = append(r.calls, call)
	r.mu.Unlock()
}

func (r *recordingCM5807) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func (r *recordingCM5807) TokenCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.tokens
}

func (r *recordingCM5807) ContextWindow() int { return 1000 }

func (r *recordingCM5807) SetContextWindow(int) {}

func (r *recordingCM5807) Messages() []provider.Message { return nil }

func (r *recordingCM5807) CheckAndSummarize(context.Context, provider.Provider) (bool, error) {
	r.record("summarize")
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.summarErr != nil {
		return false, r.summarErr
	}
	r.tokens = 50
	return true, nil
}

func (r *recordingCM5807) TruncateOldestGroupForRetry() bool {
	r.record("truncate")
	r.mu.Lock()
	r.tokens = 40
	r.mu.Unlock()
	return true
}

// TestIssue5807PTLCompactsBeforeGroupDrop pins the PTL retry ordering: a
// real overflow must attempt compact-with-summary first; per-group dropping
// only happens when the compact path is unavailable or insufficient.
func TestIssue5807PTLCompactsBeforeGroupDrop(t *testing.T) {
	ptlErr := errors.New("prompt is too long: 195000 tokens, maximum is 190000")
	onEvent := func(provider.StreamEvent) {}

	t.Run("compact available means no group drop", func(t *testing.T) {
		cm := &recordingCM5807{tokens: 5000}
		a := &Agent{contextManager: cm}
		retries := 0
		if !a.tryReactiveCompact(context.Background(), onEvent, ptlErr, &retries) {
			t.Fatal("reactive compact must succeed when summarization works")
		}
		if calls := cm.snapshot(); !reflect.DeepEqual(calls, []string{"summarize"}) {
			t.Fatalf("PTL path must compact-with-summary without dropping groups, calls=%v", calls)
		}
	})

	t.Run("group drop is the last resort", func(t *testing.T) {
		cm := &recordingCM5807{tokens: 5000, summarErr: errors.New("aux provider down")}
		a := &Agent{contextManager: cm}
		retries := 0
		if !a.tryReactiveCompact(context.Background(), onEvent, ptlErr, &retries) {
			t.Fatal("reactive compact must fall back to truncation when the compact path fails")
		}
		calls := cm.snapshot()
		if len(calls) != 2 || calls[0] != "summarize" || calls[1] != "truncate" {
			t.Fatalf("summarize must be attempted before any group drop, calls=%v", calls)
		}
	})
}
