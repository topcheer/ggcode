package provider

import (
	"context"
	"reflect"
	"testing"
)

func TestIssue2246StopSequencesWithoutTemperature(t *testing.T) {
	p := newAnthropicProvider("k", "m", 1024, "")
	p.SetTemperature(0)

	p.SetSamplingOverride(&SamplingOverride{StopSequences: []string{"END", "\n\nUSER:"}})

	params := p.buildParams(context.Background(), nil, nil)
	if !reflect.DeepEqual(params.StopSequences, []string{"END", "\n\nUSER:"}) {
		t.Fatalf("stop_sequences must reach the request body with temperature=0, got %v", params.StopSequences)
	}

	p2 := newAnthropicProvider("k", "m", 1024, "")
	if got := p2.buildParams(context.Background(), nil, nil).StopSequences; len(got) != 0 {
		t.Fatalf("no sequences configured must omit the field, got %v", got)
	}
}

func TestIssue2266OverrideSequencesReachAnthropicParams(t *testing.T) {
	p := newAnthropicProvider("k", "m", 1024, "")
	p.SetSamplingOverride(&SamplingOverride{MaxTokens: 100, StopSequences: []string{"END"}})
	params := p.buildParams(context.Background(), nil, nil)
	if !reflect.DeepEqual(params.StopSequences, []string{"END"}) {
		t.Fatalf("override sequences must reach the request body, got %v", params.StopSequences)
	}
	if params.MaxTokens != 100 {
		t.Fatalf("override maxTokens must win, got %d", params.MaxTokens)
	}
	p.SetSamplingOverride(nil)
	if got := p.buildParams(context.Background(), nil, nil).StopSequences; len(got) != 0 {
		t.Fatalf("cleared override must fall back to defaults, got %v", got)
	}
}
