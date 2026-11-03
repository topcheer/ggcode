package provider

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

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

	if e := tp.take("B"); e == nil || e.Key != "B" {
		t.Fatalf("skipped B must stay reachable, got %+v", e)
	}

	if e := tp.take("A"); e != nil {
		t.Fatalf("exhausted tape must return nil, got %+v", e)
	}
}

func TestIssue3933_FIFOFallbackServesEarliest(t *testing.T) {
	tp := &TapeProvider{mode: "replay", order: []*llmTapeEntry{
		{Key: "A", Kind: "chat"},
		{Key: "B", Kind: "chat"},
	}}
	if e := tp.take("Z"); e == nil || e.Key != "A" {
		t.Fatalf("FIFO fallback must serve the earliest entry, got %+v", e)
	}
}

type zeroEventProvider struct{ fakeTapeProvider }

func (z *zeroEventProvider) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition) (<-chan StreamEvent, error) {
	out := make(chan StreamEvent)
	close(out)
	return out, nil
}

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
