package provider

// #3933 probes: (1) a key match beyond the cursor must not strand the
// skipped middle entries; (2) a zero-event stream is recorded as a
// placeholder so replay key alignment survives.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Recorded [A, B, A], replayed [A, A, B]: the old take() jumped the cursor
// past B on the second A and take(B) hit the hard exhaustion error while B
// sat unconsumed.
func TestIssue3933_SkippedEntryStaysReachable(t *testing.T) {
	tp := &TapeProvider{mode: "replay", order: []*llmTapeEntry{
		{Key: "A", Kind: "chat"},
		{Key: "B", Kind: "chat"},
		{Key: "A", Kind: "chat"},
	}}
	if e := tp.take("A"); e == nil || e.Key != "A" {
		t.Fatal("first take(A) failed")
	}
	if e := tp.take("A"); e == nil || e.Key != "A" {
		t.Fatal("second take(A) failed")
	}
	// The skipped B must still be reachable - via FIFO fallback at minimum.
	if e := tp.take("B"); e == nil || e.Key != "B" {
		t.Fatalf("skipped B must stay reachable, got %+v", e)
	}
	// Tape is now truly exhausted.
	if e := tp.take("A"); e != nil {
		t.Fatalf("exhausted tape must return nil, got %+v", e)
	}
}

// FIFO fallback order is preserved: with no key match, the earliest
// unconsumed entry is served.
func TestIssue3933_FIFOFallbackServesEarliest(t *testing.T) {
	tp := &TapeProvider{mode: "replay", order: []*llmTapeEntry{
		{Key: "A", Kind: "chat"},
		{Key: "B", Kind: "chat"},
	}}
	if e := tp.take("Z"); e == nil || e.Key != "A" {
		t.Fatalf("FIFO fallback must serve the earliest entry, got %+v", e)
	}
}

// zeroEventProvider's stream closes before emitting anything (the ctx
// cancel-before-first-event shape).
type zeroEventProvider struct{ fakeTapeProvider }

func (z *zeroEventProvider) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition) (<-chan StreamEvent, error) {
	out := make(chan StreamEvent)
	close(out)
	return out, nil
}

// Zero-event streams are recorded (placeholder), so a replayed zero-event
// turn finds its key instead of misaligning onto the next turn's entry.
func TestIssue3933_ZeroEventStreamRecorded(t *testing.T) {
	dir := t.TempDir()
	tape := filepath.Join(dir, "tape.jsonl")
	t.Setenv("GGCODE_LLM_TAPE", "record:"+tape)
	rp, ok := WrapLLMTapeFromEnv(&zeroEventProvider{}).(*TapeProvider)
	if !ok {
		t.Fatal("record mode must wrap")
	}
	msgs := []Message{{Role: "user", Content: []ContentBlock{TextBlock("q")}}}
	out, err := rp.ChatStream(context.Background(), msgs, nil)
	if err != nil {
		t.Fatal(err)
	}
	for range out {
	}
	// The tape FILE must hold a stream entry for that key (append writes
	// the file; t.order is only populated on replay load).
	raw, err := os.ReadFile(tape)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 || !contains3933(string(raw), `"stream"`) {
		t.Fatalf("zero-event stream must be recorded as a placeholder, tape=%q", raw)
	}
}

func contains3933(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

var _ = os.Getenv
