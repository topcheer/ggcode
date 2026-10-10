package provider

import (
	"encoding/json"
	"math"
	"testing"
)

// #3938: tapeEvent projection must round-trip ALL StreamEvent business
// fields. A field that records as zero on tape makes replayed trajectories
// diverge from recorded ones (low-confidence alert lost, server-tool
// echo-back broken, thought-signature/caller echo-back broken).
func TestTapeEventFidelityRoundTrip(t *testing.T) {
	conf := -3.0
	sig := []byte("sig-bytes-1610")
	caller := json.RawMessage(`{"type":"code_execution_20260120","tool_id":"toolu_1"}`)
	ev := StreamEvent{
		Type: StreamEventDone,
		Tool: ToolCallDelta{
			ID: "tc1", Index: 2, Name: "web_search",
			ServerTool:       true,
			Arguments:        json.RawMessage(`{"q":"x"}`),
			ThoughtSignature: sig,
			Caller:           caller,
		},
		Confidence: &conf,
	}
	te := fromStreamEvent(ev)
	if te.Confidence == nil || *te.Confidence != conf {
		t.Fatalf("Confidence not recorded: %+v", te.Confidence)
	}
	if !te.ToolServerTool {
		t.Fatal("Tool.ServerTool marker not recorded")
	}
	if string(te.ToolThoughtSig) != string(sig) {
		t.Fatalf("ThoughtSignature not recorded: %q", te.ToolThoughtSig)
	}
	if te.ToolCaller != string(caller) {
		t.Fatalf("Caller not recorded: %q", te.ToolCaller)
	}
	// JSON wire round-trip (old-tape compat: absent fields decode as zero).
	raw, err := json.Marshal(te)
	if err != nil {
		t.Fatal(err)
	}
	var back tapeEvent
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	out := back.toStreamEvent()
	if out.Confidence == nil || math.Abs(*out.Confidence-conf) > 1e-9 {
		t.Fatalf("Confidence lost on replay: %+v", out.Confidence)
	}
	if !out.Tool.ServerTool || out.Tool.ID != "tc1" || out.Tool.Name != "web_search" {
		t.Fatalf("tool delta fidelity broken: %+v", out.Tool)
	}
	if string(out.Tool.ThoughtSignature) != string(sig) {
		t.Fatalf("ThoughtSignature lost on replay: %q", out.Tool.ThoughtSignature)
	}
	if string(out.Tool.Caller) != string(caller) {
		t.Fatalf("Caller lost on replay: %q", out.Tool.Caller)
	}
}

// ServerTool events must persist the verbatim ContentBlock so replay can
// echo the full server_tool_use/result pair back to the API (#3938).
func TestTapeEventServerToolBlockRoundTrip(t *testing.T) {
	ev := StreamEvent{
		Type: StreamEventServerTool,
		Block: ContentBlock{
			Type:     "server_tool_use",
			ToolID:   "srvtoolu_1",
			ToolName: "web_search",
		},
	}
	te := fromStreamEvent(ev)
	if te.Block == nil {
		t.Fatal("ServerTool block not recorded")
	}
	out := te.toStreamEvent()
	if out.Block.Type != "server_tool_use" || out.Block.ToolID != "srvtoolu_1" || out.Block.ToolName != "web_search" {
		t.Fatalf("block fidelity broken on replay: %+v", out.Block)
	}
}

// Old tapes (recorded before #3938) have no cf/blk/tst/tsig/tcal keys;
// replay must decode them without error and leave the fields zero-valued.
func TestTapeEventOldTapeBackCompat(t *testing.T) {
	old := []byte(`{"t":1,"tx":"hello","ti":"a","tn":0,"tm":"f","ta":"{}"}`)
	var te tapeEvent
	if err := json.Unmarshal(old, &te); err != nil {
		t.Fatalf("old tape entry must still decode: %v", err)
	}
	ev := te.toStreamEvent()
	if ev.Confidence != nil || ev.Tool.ServerTool || ev.Tool.ThoughtSignature != nil || ev.Tool.Caller != nil || ev.Block.Type != "" {
		t.Fatalf("old tape replay should leave new fields zero: %+v", ev)
	}
	if ev.Text != "hello" || ev.Tool.ID != "a" {
		t.Fatalf("old tape core fields broken: %+v", ev)
	}
}
