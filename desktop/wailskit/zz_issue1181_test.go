package wailskit

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func textMsg(role, text string) provider.Message {
	return provider.Message{Role: role, Content: []provider.ContentBlock{{Type: "text", Text: text}}}
}

// #1181: a duplicate finisher (interleaved Cancel race) must persist only
// the tail after the winner's watermark instead of dropping it.
func TestRunMessagesToPersistFirstPersistSkipsSeedUser(t *testing.T) {
	runAdded := []provider.Message{
		textMsg("user", "seed question"),
		textMsg("assistant", "partial answer"),
	}
	got := runMessagesToPersist(runAdded, 0)
	if len(got) != 1 || got[0].Role != "assistant" {
		b, _ := json.Marshal(got)
		t.Fatalf("expected only the assistant tail, got %s", b)
	}
}

// #1181: the second (duplicate) finisher sees skip>0 and must append only
// messages produced after the winner's persist - no drops, no duplicates.
func TestRunMessagesToPersistWatermarkSkipsPersistedPrefix(t *testing.T) {
	runAdded := []provider.Message{
		textMsg("user", "seed question"),
		textMsg("assistant", "partial answer"),
		textMsg("assistant", "tail produced after winner persisted"),
	}
	// Winner persisted 2 entries (seed+assistant) as len(runAdded)=2 at that
	// moment; the duplicate finisher now sees 3 entries.
	got := runMessagesToPersist(runAdded, 2)
	if len(got) != 1 || !strings.Contains(got[0].Content[0].Text, "tail") {
		b, _ := json.Marshal(got)
		t.Fatalf("expected only the unpersisted tail, got %s", b)
	}
}

// #1181: a watermark larger than the current list (cancel raced ahead) must
// not panic and must return nothing.
func TestRunMessagesToPersistWatermarkCapsBeyondLen(t *testing.T) {
	runAdded := []provider.Message{{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "seed"}}}}
	got := runMessagesToPersist(runAdded, 10)
	if len(got) != 0 {
		t.Fatalf("expected no messages, got %d", len(got))
	}
}

// #1181: the pre-existing no-seed-user fallback must survive the watermark
// refactor when skip == 0.
func TestRunMessagesToPersistNoSeedUserKeepsAll(t *testing.T) {
	runAdded := []provider.Message{textMsg("assistant", "only assistant")}
	got := runMessagesToPersist(runAdded, 0)
	if len(got) != 1 {
		t.Fatalf("expected all messages kept, got %d", len(got))
	}
}

// #1181: the finished-guard duplicate path must not emit run_done again and
// must not panic on the persist call with an empty bridge (no agent). The
// real-session tail-persist behavior is covered by the watermark tests and
// compile-time wiring in finishRun.
func TestFinishRunDuplicateFinisherDoesNotDoubleEmit(t *testing.T) {
	var emits int64
	b := &ChatBridge{
		finished: true, // winner already completed cleanup
		OnStreamEvent: func(_ string, _ json.RawMessage) {
			atomic.AddInt64(&emits, 1)
		},
	}
	b.finishRun(nil) // duplicate finisher: must hit the finished branch
	if n := atomic.LoadInt64(&emits); n != 0 {
		t.Fatalf("duplicate finisher emitted %d events, want 0 (double run_done)", n)
	}
}
