package provider

// #3938 probe: the tapeEvent projection must round-trip the five business
// fields it used to drop (Confidence, Block, Tool.ServerTool,
// Tool.ThoughtSignature, Tool.Caller); a projection missing them silently
// diverges replayed trajectories from recorded ones.

import (
	"encoding/json"
	"testing"
)

func TestIssue3938_ProjectionRoundTripsFiveFields(t *testing.T) {
	cf := -3.0
	in := StreamEvent{
		Type: StreamEventDone,
		Tool: ToolCallDelta{
			ID:               "call_1",
			Name:             "web_search",
			ServerTool:       true,
			ThoughtSignature: []byte("sig-1610"),
			Caller:           json.RawMessage(`{"ptc":1}`),
		},
		Confidence: &cf,
		Block: ContentBlock{
			Type: "server_tool_use",
			Raw:  json.RawMessage(`{"web_search_tool_result":{"results":[]}}`),
		},
	}
	te := fromStreamEvent(in)
	// JSON round-trip (the tape file representation).
	b, err := json.Marshal(te)
	if err != nil {
		t.Fatal(err)
	}
	var back tapeEvent
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	out := back.toStreamEvent()

	if out.Confidence == nil || *out.Confidence != cf {
		t.Fatalf("Confidence lost: %+v", out.Confidence)
	}
	if !out.Tool.ServerTool {
		t.Fatalf("Tool.ServerTool lost: %v", out.Tool.ServerTool)
	}
	if string(out.Tool.ThoughtSignature) != "sig-1610" {
		t.Fatalf("Tool.ThoughtSignature lost: %q", out.Tool.ThoughtSignature)
	}
	if string(out.Tool.Caller) != `{"ptc":1}` {
		t.Fatalf("Tool.Caller lost: %q", out.Tool.Caller)
	}
	if out.Block.Type != "server_tool_use" || len(out.Block.Raw) == 0 {
		t.Fatalf("Block lost/zero-filled: %+v", out.Block)
	}
}

// Old tapes (pre-#3938, no new keys) still load and replay to the same
// StreamEvent the old projection produced.
func TestIssue3938_OldTapeShapeStillReplays(t *testing.T) {
	legacy := `{"t":4,"tx":"hi","ti":"t1","tm":"read_file","ta":"{}"}`
	var te tapeEvent
	if err := json.Unmarshal([]byte(legacy), &te); err != nil {
		t.Fatal(err)
	}
	ev := te.toStreamEvent()
	if ev.Text != "hi" || ev.Tool.Name != "read_file" || ev.Confidence != nil || ev.Block.Type != "" {
		t.Fatalf("legacy tape replay diverged: %+v", ev)
	}
}
