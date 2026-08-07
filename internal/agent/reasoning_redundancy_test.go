package agent

import (
	"strings"
	"testing"
)

func TestReasoningRedundancy_BasicDetection(t *testing.T) {
	s := newReasoningRedundancyState()
	s.reset()

	// Shared word set repeated for high Jaccard similarity (>=0.55)
	base := "the handler processes requests routing them to service layer checking error propagation middleware response formatted returning caller structure analyze codebase examining flow architecture implementation details thoroughly reviewing code path verification testing approach understanding context model behavior patterns clearly"
	text1 := base + " first iteration examining the flow"
	text2 := base + " second iteration examining the flow"
	text3 := base + " third iteration examining the flow"
	s.recordReasoning(text1, false)
	s.recordReasoning(text2, false)

	// First two turns shouldn't trigger yet (need rrWindow=3)
	msg := s.maybeWarn(1, 10)
	if msg != "" {
		t.Fatalf("should not fire with only 2 turns, got: %s", msg)
	}

	// Third similar turn triggers
	s.recordReasoning(text3, false)
	msg = s.maybeWarn(2, 10)
	if msg == "" {
		t.Fatal("should fire after 3 consecutive text-only redundant turns")
	}
	if !strings.Contains(msg, "Reasoning Redundancy") {
		t.Errorf("message should contain detector name, got: %s", msg)
	}
}

func TestReasoningRedundancy_ToolCallResets(t *testing.T) {
	s := newReasoningRedundancyState()

	text1 := "Let me analyze the structure of this codebase. The handler processes incoming requests by routing them to the service layer. I should check how errors propagate through the middleware and whether the response is properly formatted."
	text2 := "Looking at the structure, the handler routes incoming requests to the service layer. I need to understand how errors propagate through middleware and whether responses are properly formatted."

	s.recordReasoning(text1, false)
	s.recordReasoning(text2, false)

	// Tool call breaks the streak
	s.recordReasoning("", true)

	s.recordReasoning(text1, false)
	s.recordReasoning(text2, false)

	// Only 2 turns since reset, shouldn't fire
	msg := s.maybeWarn(1, 10)
	if msg != "" {
		t.Fatalf("should not fire after tool call reset with only 2 turns, got: %s", msg)
	}
}

func TestReasoningRedundancy_ShortTextIgnored(t *testing.T) {
	s := newReasoningRedundancyState()

	// Short texts should be ignored
	s.recordReasoning("hello world short text", false)
	s.recordReasoning("hello world short text again", false)
	s.recordReasoning("hello world short text more", false)

	msg := s.maybeWarn(1, 10)
	if msg != "" {
		t.Fatalf("should not fire for short texts, got: %s", msg)
	}
}

func TestReasoningRedundancy_DissimilarTextsIgnored(t *testing.T) {
	s := newReasoningRedundancyState()

	// Three text-only turns with very different content
	text1 := strings.Repeat("alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu", 2)
	text2 := strings.Repeat("north south east west up down left right center forward backward halt", 2)
	text3 := strings.Repeat("red green blue yellow orange purple pink brown black white gray silver", 2)

	s.recordReasoning(text1, false)
	s.recordReasoning(text2, false)
	s.recordReasoning(text3, false)

	msg := s.maybeWarn(1, 10)
	if msg != "" {
		t.Fatalf("should not fire for dissimilar texts, got: %s", msg)
	}
}

func TestReasoningRedundancy_MaxWarnings(t *testing.T) {
	s := newReasoningRedundancyState()

	base := "the handler processes requests routing them to service layer checking error propagation middleware response formatted returning caller structure analyze codebase examining flow architecture implementation details thoroughly reviewing code path verification testing approach understanding context model behavior patterns clearly"
	text1 := base + " first"
	text2 := base + " second"
	text3 := base + " third"

	// First warning
	s.recordReasoning(text1, false)
	s.recordReasoning(text2, false)
	s.recordReasoning(text3, false)
	msg := s.maybeWarn(1, 10)
	if msg == "" {
		t.Fatal("first warning should fire")
	}

	// Second warning (need to add another turn)
	s.recordReasoning(text1, false)
	msg2 := s.maybeWarn(2, 10)
	if msg2 == "" {
		t.Fatal("second warning should fire")
	}

	// Third call should be suppressed (maxWarnings=2)
	s.recordReasoning(text2, false)
	msg3 := s.maybeWarn(3, 10)
	if msg3 != "" {
		t.Fatalf("should not exceed max warnings, got: %s", msg3)
	}
}

func TestReasoningRedundancy_Reset(t *testing.T) {
	s := newReasoningRedundancyState()
	s.recordReasoning("test text here with enough words to pass threshold for sure yes definitely okay now", false)
	s.reset()
	if len(s.turns) != 0 {
		t.Errorf("turns should be empty after reset, got %d", len(s.turns))
	}
	if s.warnCount != 0 {
		t.Errorf("warnCount should be 0 after reset, got %d", s.warnCount)
	}
}

func TestTokenizeForRR(t *testing.T) {
	tokens := tokenizeForRR("Hello, World! This is a TEST 123.")
	if len(tokens) != 7 {
		t.Fatalf("expected 7 tokens, got %d: %v", len(tokens), tokens)
	}
	if tokens[0] != "hello" {
		t.Errorf("expected 'hello', got '%s'", tokens[0])
	}
	if tokens[6] != "123" {
		t.Errorf("expected '123', got '%s'", tokens[6])
	}
}

func TestReasoningRedundancy_WarnCountField(t *testing.T) {
	s := newReasoningRedundancyState()

	base := "the handler processes requests routing them to service layer checking error propagation middleware response formatted returning caller structure analyze codebase examining flow architecture implementation details thoroughly reviewing code path verification testing approach understanding context model behavior patterns clearly"
	text1 := base + " first"
	text2 := base + " second"
	text3 := base + " third"

	s.recordReasoning(text1, false)
	s.recordReasoning(text2, false)
	s.recordReasoning(text3, false)
	_ = s.maybeWarn(1, 10)

	if s.warnCount != 1 {
		t.Errorf("expected warnCount=1, got %d", s.warnCount)
	}
}
