package provider

import (
	"os"
	"strings"
	"testing"
)

func TestIssue2774BetaHeaderAggregation(t *testing.T) {
	p := NewAnthropicProvider("k", "claude-x", 8192)
	p.SetThinkingMode("manual")
	p.SetReasoningEffort("high")
	p.SetServerTools([]ServerToolConfig{{Type: "code_execution_20260120"}})

	got := p.betaHeaderValue(true)
	want := "interleaved-thinking-2025-05-14,programmatic-tool-calling-2026-01-20"
	if got != want {
		t.Fatalf("betaHeaderValue = %q, want %q", got, want)
	}
	opts := p.betaHeaderOpts(true)
	if len(opts) != 1 {
		t.Fatalf("betaHeaderOpts returned %d options, want exactly 1 (single Set semantics - multiple options overwrite each other)", len(opts))
	}

	p.toolSearchBeta = true
	got = p.betaHeaderValue(true)
	want = "interleaved-thinking-2025-05-14,programmatic-tool-calling-2026-01-20,advanced-tool-use-2025-11-20"
	if got != want {
		t.Fatalf("triple-token betaHeaderValue = %q, want %q", got, want)
	}

	p2 := NewAnthropicProvider("k", "claude-x", 8192)
	p2.SetServerTools([]ServerToolConfig{{Type: "code_execution_20260120"}})
	if got := p2.betaHeaderValue(false); got != "programmatic-tool-calling-2026-01-20" {
		t.Fatalf("no-tools betaHeaderValue = %q, want PTC only", got)
	}

	p3 := NewAnthropicProvider("k", "claude-x", 8192)
	if got := p3.betaHeaderValue(true); got != "" {
		t.Fatalf("inactive betaHeaderValue = %q, want empty", got)
	}
	if opts := p3.betaHeaderOpts(true); len(opts) != 0 {
		t.Fatalf("inactive betaHeaderOpts = %d options, want 0", len(opts))
	}
}

func TestIssue2774RetryPathsKeepFullBetaSet(t *testing.T) {
	src, err := readFileForIssue2774("anthropic.go")
	if err != nil {
		t.Fatalf("read anthropic.go: %v", err)
	}

	if n := strings.Count(src, `WithHeader("anthropic-beta"`); n != 1 {
		t.Fatalf("anthropic.go has %d WithHeader(\"anthropic-beta\") emitters, want exactly 1 (the aggregator) - split emitters overwrite each other per SDK Set semantics (#2774)", n)
	}

	if strings.Contains(src, "callOpts = nil") {
		t.Fatal("callOpts = nil found - retry paths must keep the full beta-header option set (#2774 Bug B)")
	}
}

func readFileForIssue2774(name string) (string, error) {
	data, err := os.ReadFile(name)
	return string(data), err
}
