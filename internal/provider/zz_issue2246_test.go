package provider

// #2246 regression: the #2239 stop_sequences assignment landed INSIDE
// the temperature>0 guard in buildParams, so the default config
// (temperature 0) silently dropped MCP sampling stop sequences on
// Anthropic. The #2243 tests used a stub provider and never serialized
// the real params - this pin goes straight through buildParams.

import (
	"reflect"
	"testing"
)

func TestIssue2246StopSequencesWithoutTemperature(t *testing.T) {
	p := newAnthropicProvider("k", "m", 1024, "")
	p.SetTemperature(0) // explicit default - the guard must not eat the sequences
	p.SetStopSequences([]string{"END", "\n\nUSER:"})

	params := p.buildParams(nil, nil)
	if !reflect.DeepEqual(params.StopSequences, []string{"END", "\n\nUSER:"}) {
		t.Fatalf("stop_sequences must reach the request body with temperature=0, got %v", params.StopSequences)
	}

	// And the empty case stays absent (omitempty contract).
	p2 := newAnthropicProvider("k", "m", 1024, "")
	if got := p2.buildParams(nil, nil).StopSequences; len(got) != 0 {
		t.Fatalf("no sequences configured must omit the field, got %v", got)
	}
}

func TestIssue2266OverrideSequencesReachAnthropicParams(t *testing.T) {
	p := newAnthropicProvider("k", "m", 1024, "")
	p.SetSamplingOverride(&SamplingOverride{MaxTokens: 100, StopSequences: []string{"END"}})
	params := p.buildParams(nil, nil)
	if !reflect.DeepEqual(params.StopSequences, []string{"END"}) {
		t.Fatalf("override sequences must reach the request body, got %v", params.StopSequences)
	}
	if params.MaxTokens != 100 {
		t.Fatalf("override maxTokens must win, got %d", params.MaxTokens)
	}
	p.SetSamplingOverride(nil)
	if got := p.buildParams(nil, nil).StopSequences; len(got) != 0 {
		t.Fatalf("cleared override must fall back to defaults, got %v", got)
	}
}
