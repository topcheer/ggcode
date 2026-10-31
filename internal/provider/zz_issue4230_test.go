package provider

import (
	"encoding/json"
	"testing"
)

// #4230 case A: chatResponseFromPayload must preserve generation order.
// Model outputs [message, function_call]; the old collect-texts-then-append-
// at-end reversed the reconstructed Content to [tool_use, text].
func TestIssue4230_PayloadContentOrderFidelity(t *testing.T) {
	payload := &responsesPayload{
		Output: []json.RawMessage{
			json.RawMessage(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Let me check that."}]}`),
			json.RawMessage(`{"type":"function_call","call_id":"call_1","name":"grep","arguments":"{\"pattern\":\"x\"}"}`),
		},
	}
	resp := chatResponseFromPayload(payload)
	if len(resp.Message.Content) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(resp.Message.Content))
	}
	if resp.Message.Content[0].Type != "text" {
		t.Fatalf("block 0 must be text (generation order), got %s", resp.Message.Content[0].Type)
	}
	if resp.Message.Content[1].Type != "tool_use" {
		t.Fatalf("block 1 must be tool_use, got %s", resp.Message.Content[1].Type)
	}
}

// #4230 case B: usage details (cached_tokens) must survive the payload path
// so background/non-stream cost accounting gets the cache discount.
func TestIssue4230_UsageCacheReadDecoded(t *testing.T) {
	payload := &responsesPayload{}
	if err := json.Unmarshal([]byte(`{"usage":{"input_tokens":100,"output_tokens":5,"input_tokens_details":{"cached_tokens":80}}}`), &payload); err != nil {
		t.Fatal(err)
	}
	resp := chatResponseFromPayload(payload)
	if resp.Usage.CacheRead != 80 {
		t.Fatalf("CacheRead must decode input_tokens_details.cached_tokens, got %d", resp.Usage.CacheRead)
	}
}
