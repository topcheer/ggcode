package provider

import (
	"encoding/json"
	"testing"
)

func TestResponsesHostedToolRecognizesApplyPatch(t *testing.T) {
	tl, ok := responsesHostedTool(ServerToolConfig{Type: "apply_patch"})
	if !ok {
		t.Fatal("apply_patch must be recognized by SetServerTools")
	}
	if tl.Type != "apply_patch" {
		t.Fatalf("type: %q", tl.Type)
	}
	// Fail closed for garbage.
	if _, ok := responsesHostedTool(ServerToolConfig{Type: "code_interpreter"}); ok {
		t.Fatal("unknown server tool must be rejected")
	}
}

func TestDecodeResponsesApplyPatchCall(t *testing.T) {
	raw := json.RawMessage(`{"type":"apply_patch_call","id":"ap_1","call_id":"call_abc","status":"in_progress",
		"operation":{"type":"create_file","path":"a.txt","diff":"*** Add File: a.txt\n+x\n"}}`)
	id, op, ok := decodeResponsesApplyPatchCall(raw)
	if !ok || id != "call_abc" {
		t.Fatalf("decode: ok=%v id=%q", ok, id)
	}
	if len(op) == 0 || string(op) == "null" {
		t.Fatalf("operation lost: %s", op)
	}
	// id fallback when call_id missing.
	raw2 := json.RawMessage(`{"type":"apply_patch_call","id":"ap_2","operation":null}`)
	if id, _, ok := decodeResponsesApplyPatchCall(raw2); !ok || id != "ap_2" {
		t.Fatalf("id fallback: ok=%v id=%q", ok, id)
	}
	// Non-apply items rejected.
	for _, bad := range []json.RawMessage{
		json.RawMessage(`{"type":"function_call","call_id":"c"}`),
		json.RawMessage(`{"type":"web_search_call"}`),
		json.RawMessage(`{"type":"apply_patch_call"}`), // no id at all
		nil,
	} {
		if _, _, ok := decodeResponsesApplyPatchCall(bad); ok {
			t.Fatalf("must reject: %s", bad)
		}
	}
}

func TestResponsesApplyPatchReplays(t *testing.T) {
	op := json.RawMessage(`{"type":"update_file","path":"f.go","diff":"x"}`)
	call := responsesApplyPatchCallReplay("call_1", op)
	var m map[string]any
	if err := json.Unmarshal(call, &m); err != nil {
		t.Fatalf("call replay not JSON: %v", err)
	}
	if m["type"] != "apply_patch_call" || m["call_id"] != "call_1" || m["status"] != "completed" {
		t.Fatalf("call replay shape: %s", call)
	}
	// Operation rides through verbatim.
	var withOp struct {
		Operation json.RawMessage `json:"operation"`
	}
	if err := json.Unmarshal(call, &withOp); err != nil || string(withOp.Operation) != string(op) {
		t.Fatalf("operation not embedded: %s err=%v", call, err)
	}
	// Corrupt input degrades to no operation, valid JSON still.
	if err := json.Unmarshal(responsesApplyPatchCallReplay("c", json.RawMessage("{bad")), &m); err != nil {
		t.Fatalf("corrupt replay not JSON: %v", err)
	}

	out := responsesApplyPatchOutputReplay("call_1", "boom", true)
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("output replay not JSON: %v", err)
	}
	if m["type"] != "apply_patch_call_output" || m["status"] != "failed" || m["output"] != "boom" {
		t.Fatalf("output replay shape: %s", out)
	}
	if err := json.Unmarshal(responsesApplyPatchOutputReplay("c", "ok", false), &m); err != nil || m["status"] != "completed" {
		t.Fatalf("success output: %s err=%v", m, err)
	}
}

func TestBuildResponsesInputApplyPatchRoundTrip(t *testing.T) {
	op := `{"type":"create_file","path":"a.txt","diff":"*** Add File: a.txt\n+x\n"}`
	messages := []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("patch it")}},
		{Role: "assistant", Content: []ContentBlock{
			ToolUseBlock("call_ap", applyPatchInternalToolName, json.RawMessage(op)),
		}},
		{Role: "tool", Content: []ContentBlock{
			{Type: "tool_result", ToolID: "call_ap", Output: "Created a.txt (1 lines)"},
		}},
		// Ordinary function call must be untouched.
		{Role: "assistant", Content: []ContentBlock{
			ToolUseBlock("call_fn", "read_file", json.RawMessage(`{"path":"a.txt"}`)),
		}},
		{Role: "tool", Content: []ContentBlock{
			{Type: "tool_result", ToolID: "call_fn", Output: "contents"},
		}},
	}
	items := buildResponsesInput(messages)
	if len(items) != 5 {
		t.Fatalf("want 5 items, got %d: %+v", len(items), items)
	}
	got := make([]string, 0, len(items))
	for _, it := range items {
		b, _ := json.Marshal(it)
		got = append(got, string(b))
	}
	// item 2: replayed apply_patch_call
	var callItem map[string]any
	if err := json.Unmarshal([]byte(got[1]), &callItem); err != nil {
		t.Fatalf("item1: %v", err)
	}
	if callItem["type"] != "apply_patch_call" || callItem["call_id"] != "call_ap" {
		t.Fatalf("apply_patch_call replay: %s", got[1])
	}
	// item 3: apply_patch_call_output
	var outItem map[string]any
	if err := json.Unmarshal([]byte(got[2]), &outItem); err != nil {
		t.Fatalf("item2: %v", err)
	}
	if outItem["type"] != "apply_patch_call_output" || outItem["status"] != "completed" || outItem["output"] != "Created a.txt (1 lines)" {
		t.Fatalf("apply_patch_call_output replay: %s", got[2])
	}
	// item 4/5: ordinary function call path unchanged.
	var fnItem, fnOut map[string]any
	json.Unmarshal([]byte(got[3]), &fnItem)
	json.Unmarshal([]byte(got[4]), &fnOut)
	if fnItem["type"] != "function_call" || fnItem["name"] != "read_file" {
		t.Fatalf("function_call: %s", got[3])
	}
	if fnOut["type"] != "function_call_output" {
		t.Fatalf("function_call_output: %s", got[4])
	}
}
