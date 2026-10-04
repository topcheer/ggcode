package provider

// Structured outputs (response_format=json_schema strict): the final
// assistant response is constrained to a JSON Schema. sa-228 gap: the
// repo had zero constrained-decoding support and was forced to
// post-hoc repair free-text JSON (jsonrepair.go); this pins the
// request-side contract.

import (
	"encoding/json"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestOpenAIApplyResponseSchema(t *testing.T) {
	p := NewOpenAIProviderWithBaseURL("k", "gpt-5", 0, "http://localhost")

	// No schema installed: no-op.
	req := openai.ChatCompletionRequest{}
	p.applyResponseSchema(&req)
	if req.ResponseFormat != nil {
		t.Fatalf("apply with no schema must be a no-op, got %+v", req.ResponseFormat)
	}

	schema := json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string"}},"required":["summary"],"additionalProperties":false}`)
	p.SetResponseSchema(schema)
	if string(p.ResponseSchema()) != string(schema) {
		t.Fatalf("getter round-trip mismatch")
	}

	req = openai.ChatCompletionRequest{}
	p.applyResponseSchema(&req)
	if req.ResponseFormat == nil || req.ResponseFormat.JSONSchema == nil {
		t.Fatalf("response_format.json_schema not injected")
	}
	js := req.ResponseFormat.JSONSchema
	if !js.Strict {
		t.Errorf("strict must be true")
	}
	if js.Name != "final_answer" {
		t.Errorf("schema name = %q, want final_answer", js.Name)
	}
	// Schema must marshal through verbatim.
	blob, err := js.Schema.MarshalJSON()
	if err != nil {
		t.Fatalf("schema marshal: %v", err)
	}
	var got, want map[string]any
	if err := json.Unmarshal(blob, &got); err != nil {
		t.Fatalf("schema blob not valid JSON: %v", err)
	}
	_ = json.Unmarshal(schema, &want)
	if got["type"] != "object" {
		t.Errorf("schema type = %v, want object", got["type"])
	}

	// Clearing restores unconstrained behavior.
	p.SetResponseSchema(nil)
	req = openai.ChatCompletionRequest{}
	p.applyResponseSchema(&req)
	if req.ResponseFormat != nil {
		t.Fatalf("clearing schema must remove response_format")
	}
}
