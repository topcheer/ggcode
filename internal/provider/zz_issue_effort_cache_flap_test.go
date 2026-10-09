package provider

// Effort cache-flap guard (audit round 6: adaptive_effort × cache_efficiency).
//
// Official Anthropic effort guidance (2026): a top-level output_config.effort
// change does not preserve cached prefixes from earlier turns. These tests
// pin the two guarantees that keep the on-the-wire effort constant:
//  1. an established carrier survives transient per-turn deviations
//  2. the adaptive bypass latches its first level instead of tracking the
//     per-turn oscillation of the adaptive-effort adapter
//
// The real request path pairs beginEffortTracking with buildParams, so every
// scenario here drives both in that order.

import (
	"context"
	"testing"

	anthropic "github.com/anthropics/anthropic-sdk-go"
)

func TestEstablishedCarrierSurvivesTransientDeviation(t *testing.T) {
	p := newCarrierProvider()
	p.beginEffortTracking()
	p.beginEffortTracking() // established at high
	// Adaptive-adapter transient: a one-off low turn between high turns
	// updates lastCallEffort out of sync with the established level. The
	// carrier must stay attached — dropping it for one request would flip
	// the top-level value to the model default and restart the cache.
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
	// Per-turn oscillation of the adaptive-effort adapter: no two consecutive
	// requests share a level, so the hysteresis carrier never establishes and
	// the bypass serves every request. The wire value must stay latched at
	// the first observed level instead of flapping per call.
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
	p.buildParams(context.Background(), nil, nil) // bypass latches low
	// A persistent user switch re-establishes conversationEffort through the
	// hysteresis path; the established carrier must beat the latch (one
	// deliberate cache rewrite, then constant again).
	p.SetReasoningEffort("high")
	p.beginEffortTracking()
	p.beginEffortTracking()
	params := p.buildParams(context.Background(), nil, nil)
	if params.OutputConfig.Effort != anthropic.OutputConfigEffortHigh {
		t.Fatalf("established carrier must beat the latch: %q, want high", params.OutputConfig.Effort)
	}
}
