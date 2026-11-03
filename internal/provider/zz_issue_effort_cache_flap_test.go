package provider

import (
	"context"
	"testing"

	anthropic "github.com/anthropics/anthropic-sdk-go"
)

func TestEstablishedCarrierSurvivesTransientDeviation(t *testing.T) {
	p := newCarrierProvider()
	p.beginEffortTracking()
	p.beginEffortTracking()

	p.SetReasoningEffort("low")
	p.beginEffortTracking()
	params := p.buildParams(context.Background(), nil, nil)
	if params.OutputConfig.Effort != anthropic.OutputConfigEffortHigh {
		t.Fatalf("transient deviation flipped the carrier: %q, want high", params.OutputConfig.Effort)
	}
}

func TestAdaptiveBypassLatchesFirstEffort(t *testing.T) {
	p := &AnthropicProvider{maxTokens: 64000, thinkingMode: "adaptive"}
	p.effortCarrier.Store(true)

	seen := []string{}
	for _, level := range []string{"low", "high", "low", "medium", "high"} {
		p.SetReasoningEffort(level)
		p.beginEffortTracking()
		params := p.buildParams(context.Background(), nil, nil)
		seen = append(seen, string(params.OutputConfig.Effort))
	}
	for i, got := range seen {
		if got != "low" {
			t.Fatalf("wire effort flapped: %v (round %d = %q, want constant low)", seen, i, got)
		}
	}
}

func TestAdaptiveLatchYieldsToEstablishedCarrier(t *testing.T) {
	p := &AnthropicProvider{maxTokens: 64000, thinkingMode: "adaptive"}
	p.effortCarrier.Store(true)
	p.SetReasoningEffort("low")
	p.beginEffortTracking()
	p.buildParams(context.Background(), nil, nil)

	p.SetReasoningEffort("high")
	p.beginEffortTracking()
	p.beginEffortTracking()
	params := p.buildParams(context.Background(), nil, nil)
	if params.OutputConfig.Effort != anthropic.OutputConfigEffortHigh {
		t.Fatalf("established carrier must beat the latch: %q, want high", params.OutputConfig.Effort)
	}
}
