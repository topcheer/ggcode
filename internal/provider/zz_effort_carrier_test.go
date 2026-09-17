package provider

// Effort carrier (top-level output_config.effort) tests.
//
// Cache contract (Anthropic effort guidance, 2026): a top-level effort
// change does not preserve cached prefixes, so the carrier must only be
// attached for user-established, stable effort levels — never for the
// per-turn oscillation produced by the adaptive-effort adapter.

import (
	"errors"
	"testing"

	anthropic "github.com/anthropics/anthropic-sdk-go"
)

// newCarrierProvider returns a provider with the carrier enabled and no
// stability history.
func newCarrierProvider() *AnthropicProvider {
	p := &AnthropicProvider{maxTokens: 64000}
	p.SetReasoningEffort("high")
	p.effortCarrier.Store(true)
	return p
}

func TestSetReasoningEffortAcceptsXhighAndMax(t *testing.T) {
	p := &AnthropicProvider{maxTokens: 64000}
	for _, level := range []string{"xhigh", "max", "XHIGH", " High "} {
		p.SetReasoningEffort(level)
		if p.ReasoningEffort() != normalizeEffortForTest(level) {
			t.Errorf("SetReasoningEffort(%q) = %q, want %q", level, p.ReasoningEffort(), normalizeEffortForTest(level))
		}
	}
	// Invalid levels are still ignored.
	p.SetReasoningEffort("turbo")
	if p.ReasoningEffort() != "high" {
		t.Errorf("invalid effort overwrote existing level: %q", p.ReasoningEffort())
	}
}

func normalizeEffortForTest(s string) string {
	return map[string]string{"xhigh": "xhigh", "max": "max", "XHIGH": "xhigh", " High ": "high"}[s]
}

func TestEffortCarrierHysteresis(t *testing.T) {
	t.Run("disabled by default", func(t *testing.T) {
		p := &AnthropicProvider{maxTokens: 64000}
		p.SetReasoningEffort("high")
		for i := 0; i < 3; i++ {
			if p.beginEffortTracking() {
				t.Fatalf("call %d: carrier must stay off when disabled", i)
			}
		}
	})

	t.Run("user-established level stabilizes on second call", func(t *testing.T) {
		p := newCarrierProvider()
		if p.beginEffortTracking() {
			t.Fatal("first call must not attach the carrier")
		}
		if !p.beginEffortTracking() {
			t.Fatal("second consecutive call at the same level must attach the carrier")
		}
		if !p.beginEffortTracking() {
			t.Fatal("third call must keep attaching the carrier")
		}
	})

	t.Run("constant applied+restored level stabilizes", func(t *testing.T) {
		// Adaptive-effort pattern with base "": apply high before each call,
		// restore "" after. Every REQUEST is at high, so the top-level
		// carrier is constant across requests — attaching is cache-correct.
		p := newCarrierProvider()
		for i := 0; i < 4; i++ {
			p.SetReasoningEffort("high")
			attached := p.beginEffortTracking()
			if i == 0 && attached {
				t.Fatal("first request must open the window, not attach")
			}
			if i > 0 && !attached {
				t.Fatalf("round %d: constant request level must attach", i)
			}
			p.SetReasoningEffort("")
		}
	})

	t.Run("alternating request levels never stabilize", func(t *testing.T) {
		p := newCarrierProvider()
		for i := 0; i < 10; i++ {
			level := "high"
			if i%2 == 1 {
				level = "low"
			}
			p.SetReasoningEffort(level)
			if p.beginEffortTracking() {
				t.Fatalf("round %d (%s): alternating levels attached the carrier", i, level)
			}
		}
	})

	t.Run("user switch re-establishes after one suppressed call", func(t *testing.T) {
		p := newCarrierProvider()
		p.beginEffortTracking()
		p.beginEffortTracking() // established at high
		p.SetReasoningEffort("medium")
		if p.beginEffortTracking() {
			t.Fatal("first call after a switch must not attach the new level")
		}
		if !p.beginEffortTracking() {
			t.Fatal("second call after a switch must re-establish the carrier")
		}
	})

	t.Run("rejection latch disables permanently", func(t *testing.T) {
		p := newCarrierProvider()
		p.beginEffortTracking()
		if !p.effortCarrier.CompareAndSwap(true, false) {
			t.Fatal("latch pre-state must be true")
		}
		if p.beginEffortTracking() {
			t.Fatal("carrier must stay off after the endpoint rejected output_config")
		}
	})
}

func TestBuildParamsOutputConfigCarrier(t *testing.T) {
	t.Run("attached for established level", func(t *testing.T) {
		p := newCarrierProvider()
		p.beginEffortTracking()
		p.beginEffortTracking()
		params := p.buildParams(nil, nil)
		if params.OutputConfig.Effort != anthropic.OutputConfigEffortHigh {
			t.Errorf("OutputConfig.Effort = %q, want %q", params.OutputConfig.Effort, anthropic.OutputConfigEffortHigh)
		}
	})

	t.Run("suppressed during stability window", func(t *testing.T) {
		p := newCarrierProvider()
		p.beginEffortTracking() // first call: window opens
		params := p.buildParams(nil, nil)
		if params.OutputConfig.Effort != "" {
			t.Errorf("first call attached carrier: %q", params.OutputConfig.Effort)
		}
	})

	t.Run("suppressed when disabled", func(t *testing.T) {
		p := &AnthropicProvider{maxTokens: 64000}
		p.SetReasoningEffort("high")
		p.lastCallEffort = "high"
		p.conversationEffort = "high"
		params := p.buildParams(nil, nil)
		if params.OutputConfig.Effort != "" {
			t.Errorf("disabled provider attached carrier: %q", params.OutputConfig.Effort)
		}
	})
}

func TestIsEffortError(t *testing.T) {
	trueCases := []struct {
		name string
		err  error
	}{
		{"unknown parameter", errors.New("[400] unexpected field output_config.effort")},
		{"gateway unknown param", errors.New("output_config is not a valid parameter")},
		{"per-turn rejection", errors.New("[400] per-turn effort is not accepted by this endpoint")},
		{"no status, anchored", errors.New("output_config not supported by this provider")},
		{"typed 422", statusErr{422, "output_config: unrecognized request field"}},
	}
	for _, tc := range trueCases {
		if !isEffortError(tc.err) {
			t.Errorf("%s: expected true, got false", tc.name)
		}
	}
	falseCases := []struct {
		name string
		err  error
	}{
		// Bare errors carry no extractable status, so the anchored-phrase
		// fallback applies (same design as isThinkingError); the negative
		// cases that matter are typed-status errors, which production SDK
		// errors always are.
		{"typed 500 with anchor", statusErr{500, "output_config internal error"}},
		{"typed 429 quota", statusErr{429, "output_config quota exceeded"}},
		{"unrelated 400", errors.New("[400] messages: field required")},
		{"nil", nil},
	}
	for _, tc := range falseCases {
		if isEffortError(tc.err) {
			t.Errorf("%s: expected false, got true", tc.name)
		}
	}
}

func TestCloneWithModelInheritsLatchResetsWindow(t *testing.T) {
	parent := newCarrierProvider()
	parent.beginEffortTracking()
	parent.beginEffortTracking() // established at high

	clone := parent.CloneWithModel("claude-sonnet-4-5").(*AnthropicProvider)
	if !clone.effortCarrier.Load() {
		t.Fatal("clone must inherit the enabled latch")
	}
	if clone.lastCallEffort != "" || clone.conversationEffort != "" {
		t.Fatalf("clone must reset the stability window, got last=%q conv=%q", clone.lastCallEffort, clone.conversationEffort)
	}

	// A rejected-output_config endpoint keeps the latch off across clones.
	parent.effortCarrier.Store(false)
	clone2 := parent.CloneWithModel("claude-opus-4-6").(*AnthropicProvider)
	if clone2.effortCarrier.Load() {
		t.Fatal("clone must inherit the disabled latch")
	}
}
