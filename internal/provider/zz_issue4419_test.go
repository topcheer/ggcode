//go:build goolm

package provider

// #4419: gemini/openai CloneWithModel dropped responseSchema (plain slice
// zero-value) and samplingOverride (atomic.Pointer literal = zero state) -
// the same trap #3066 fixed for anthropic; a model-override subagent clone
// silently lost its structured-output constraint and sampling hints.

import (
	"encoding/json"
	"testing"
)

func TestIssue4419_GeminiCloneKeepsSchemaAndSampling(t *testing.T) {
	p := &GeminiProvider{}
	p.SetResponseSchema(json.RawMessage(`{"type":"object"}`))
	p.SetSamplingOverride(&SamplingOverride{MaxTokens: 128, Temperature: 0.2})
	clone, ok := p.CloneWithModel("gemini-other").(*GeminiProvider)
	if !ok {
		t.Fatal("clone type")
	}
	if string(clone.responseSchema) != string(p.responseSchema) {
		t.Fatalf("responseSchema lost on clone: %q (#4419)", clone.responseSchema)
	}
	ov := clone.samplingOverride.Load()
	if ov == nil || ov.MaxTokens != 128 || ov.Temperature != 0.2 {
		t.Fatalf("samplingOverride lost on clone: %+v (#4419)", ov)
	}
	// Deep-copy guard: mutating the clone's schema must not touch the parent.
	clone.responseSchema[0] = ' '
	if p.responseSchema[0] != '{' {
		t.Fatalf("responseSchema must be copied, not shared (#4419)")
	}
}

func TestIssue4419_OpenAICloneKeepsSchemaAndSampling(t *testing.T) {
	p := &OpenAIProvider{}
	p.SetResponseSchema(json.RawMessage(`{"type":"object"}`))
	p.SetSamplingOverride(&SamplingOverride{MaxTokens: 64})
	clone, ok := p.CloneWithModel("gpt-other").(*OpenAIProvider)
	if !ok {
		t.Fatal("clone type")
	}
	if string(clone.responseSchema) != string(p.responseSchema) {
		t.Fatalf("responseSchema lost on clone: %q (#4419)", clone.responseSchema)
	}
	ov := clone.samplingOverride.Load()
	if ov == nil || ov.MaxTokens != 64 {
		t.Fatalf("samplingOverride lost on clone: %+v (#4419)", ov)
	}
	clone.responseSchema[0] = ' '
	if p.responseSchema[0] != '{' {
		t.Fatalf("responseSchema must be copied, not shared (#4419)")
	}
}
