package provider

// sa-44 companion: LLM response cassette round-trip. Record mode captures
// Chat + ChatStream through a fake inner provider; a fresh replay-mode
// wrapper must serve byte-equivalent responses with zero inner calls, and
// tape exhaustion must fail closed (hard error, never a live call).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type fakeTapeProvider struct {
	chatCalls, streamCalls int
}

func (f *fakeTapeProvider) Name() string { return "fake" }

func (f *fakeTapeProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResponse, error) {
	f.chatCalls++
	return &ChatResponse{
		Message:    Message{Role: "assistant", Content: []ContentBlock{{Type: "text", Text: "recorded-chat"}}},
		StopReason: "end_turn",
	}, nil
}

func (f *fakeTapeProvider) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition) (<-chan StreamEvent, error) {
	f.streamCalls++
	out := make(chan StreamEvent, 3)
	out <- StreamEvent{Type: StreamEventText, Text: "chunk"}
	out <- StreamEvent{Type: StreamEventToolCallDone, Tool: ToolCallDelta{ID: "t1", Name: "read_file", Arguments: json.RawMessage(`{"path":"/x"}`)}}
	out <- StreamEvent{Type: StreamEventDone, Usage: &TokenUsage{InputTokens: 10, OutputTokens: 5}, Truncated: true}
	close(out)
	return out, nil
}

func (f *fakeTapeProvider) CountTokens(ctx context.Context, messages []Message) (int, error) {
	return 42, nil
}

func TestLLMTapeRecordReplayRoundTrip(t *testing.T) {
	dir := t.TempDir()
	tape := filepath.Join(dir, "traj.llmtape.jsonl")

	// Record phase.
	rec := &fakeTapeProvider{}
	t.Setenv("GGCODE_LLM_TAPE", "record:"+tape)
	rp := WrapLLMTapeFromEnv(rec)
	if rp == rec {
		t.Fatal("record mode must wrap the provider")
	}
	msgs := []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hello"}}}}
	tools := []ToolDefinition{{Name: "read_file"}}
	cr, err := rp.Chat(context.Background(), msgs, tools)
	if err != nil || cr.Message.Content[0].Text != "recorded-chat" {
		t.Fatalf("record chat: %v %+v", err, cr)
	}
	sev, err := rp.ChatStream(context.Background(), msgs, tools)
	if err != nil {
		t.Fatal(err)
	}
	var got []StreamEvent
	for ev := range sev {
		got = append(got, ev)
	}
	if len(got) != 3 {
		t.Fatalf("stream events: %d", len(got))
	}
	if rec.chatCalls != 1 || rec.streamCalls != 1 {
		t.Fatalf("inner calls: chat=%d stream=%d", rec.chatCalls, rec.streamCalls)
	}

	// Replay phase: identical requests must be served with ZERO inner calls.
	rep := &fakeTapeProvider{}
	t.Setenv("GGCODE_LLM_TAPE", "replay:"+tape)
	pp := WrapLLMTapeFromEnv(rep)
	cr2, err := pp.Chat(context.Background(), msgs, tools)
	if err != nil || cr2.Message.Content[0].Text != "recorded-chat" {
		t.Fatalf("replay chat: %v %+v", err, cr2)
	}
	if cr2.StopReason != "end_turn" {
		t.Fatalf("stop reason lost: %q", cr2.StopReason)
	}
	sev2, err := pp.ChatStream(context.Background(), msgs, tools)
	if err != nil {
		t.Fatal(err)
	}
	var got2 []StreamEvent
	for ev := range sev2 {
		got2 = append(got2, ev)
	}
	if len(got2) != 3 {
		t.Fatalf("replay stream events: %d", len(got2))
	}
	if got2[0].Text != "chunk" || got2[1].Tool.Name != "read_file" || got2[1].Tool.ID != "t1" {
		t.Fatalf("replay stream fidelity: %+v", got2)
	}
	if got2[2].Usage == nil || got2[2].Usage.InputTokens != 10 || !got2[2].Truncated {
		t.Fatalf("replay usage/truncated fidelity: %+v", got2[2])
	}
	if rep.chatCalls != 0 || rep.streamCalls != 0 {
		t.Fatalf("replay must not hit inner: chat=%d stream=%d", rep.chatCalls, rep.streamCalls)
	}

	// Exhaustion fails closed.
	if _, err := pp.Chat(context.Background(), msgs, tools); err == nil {
		t.Fatal("tape exhaustion must be a hard error, not a silent live call")
	}
}

func TestLLMTapeUnsetOrMalformedPassesThrough(t *testing.T) {
	f := &fakeTapeProvider{}
	t.Setenv("GGCODE_LLM_TAPE", "")
	if got := WrapLLMTapeFromEnv(f); got != f {
		t.Fatal("unset env must return provider unchanged")
	}
	t.Setenv("GGCODE_LLM_TAPE", "bogus")
	if got := WrapLLMTapeFromEnv(f); got != f {
		t.Fatal("malformed env must fail open to the live provider")
	}
}

func TestLLMTapeRequestKeyIgnoresMessageIDs(t *testing.T) {
	mk := func(id string) []Message {
		return []Message{{ID: id, Role: "user", Content: []ContentBlock{{Type: "text", Text: "same"}}}}
	}
	if requestKey(mk("msg_AAA"), nil) != requestKey(mk("msg_BBB"), nil) {
		t.Fatal("request fingerprint must be stable across regenerated message IDs")
	}
	if requestKey(mk("msg_AAA"), nil) == requestKey(mk("msg_AAA"), []ToolDefinition{{Name: "grep"}}) {
		t.Fatal("different tool sets must fingerprint differently")
	}
}

func TestLLMTapeRecordedFileIsJSONL(t *testing.T) {
	dir := t.TempDir()
	tape := filepath.Join(dir, "t.jsonl")
	t.Setenv("GGCODE_LLM_TAPE", "record:"+tape)
	rp := WrapLLMTapeFromEnv(&fakeTapeProvider{})
	msgs := []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "q"}}}}
	if _, err := rp.Chat(context.Background(), msgs, nil); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(tape)
	if err != nil || len(raw) == 0 {
		t.Fatalf("tape file: %v", err)
	}
	var e llmTapeEntry
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatalf("tape must be one JSON object per line: %v", err)
	}
	if e.Kind != "chat" || e.Response == nil {
		t.Fatalf("entry kind=%q resp=%v", e.Kind, e.Response)
	}
}
