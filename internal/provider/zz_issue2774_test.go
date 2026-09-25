package provider

import (
	"os"
	"strings"
	"testing"
)

// TestIssue2774BetaHeaderAggregation pins #2774: every enabled
// anthropic-beta token (interleaved-thinking for manual thinking + tools,
// programmatic-tool-calling for the PTC server tool, advanced-tool-use for
// tool search) must aggregate into ONE comma-separated anthropic-beta
// header value. The pre-fix code emitted multiple option.WithHeader calls
// on the same key - SDK WithHeader is Header.Set (last one wins), so the
// PTC token silently dropped the interleaved-thinking token (hard 400 when
// both features are configured), and the thinking/effort retry paths
// rebuilt options without the PTC header while params still declared the
// code_execution tool.
func TestIssue2774BetaHeaderAggregation(t *testing.T) {
	p := NewAnthropicProvider("k", "claude-x", 8192)
	p.SetThinkingMode("manual")  // force budget_tokens carrier (auto-detect picks adaptive for this model)
	p.SetReasoningEffort("high") // manual thinking budget > 0
	p.SetServerTools([]ServerToolConfig{{Type: "code_execution_20260120"}})

	// PTC + manual thinking + tools: both tokens, comma-joined, in ONE option.
	got := p.betaHeaderValue(true)
	want := "interleaved-thinking-2025-05-14,programmatic-tool-calling-2026-01-20"
	if got != want {
		t.Fatalf("betaHeaderValue = %q, want %q", got, want)
	}
	opts := p.betaHeaderOpts(true)
	if len(opts) != 1 {
		t.Fatalf("betaHeaderOpts returned %d options, want exactly 1 (single Set semantics - multiple options overwrite each other)", len(opts))
	}

	// Tool search joins the same aggregate (triple-token form).
	p.toolSearchBeta = true
	got = p.betaHeaderValue(true)
	want = "interleaved-thinking-2025-05-14,programmatic-tool-calling-2026-01-20,advanced-tool-use-2025-11-20"
	if got != want {
		t.Fatalf("triple-token betaHeaderValue = %q, want %q", got, want)
	}

	// hasTools=false: no interleaved token, PTC still applies.
	p2 := NewAnthropicProvider("k", "claude-x", 8192)
	p2.SetServerTools([]ServerToolConfig{{Type: "code_execution_20260120"}})
	if got := p2.betaHeaderValue(false); got != "programmatic-tool-calling-2026-01-20" {
		t.Fatalf("no-tools betaHeaderValue = %q, want PTC only", got)
	}

	// Nothing enabled: empty value, zero options.
	p3 := NewAnthropicProvider("k", "claude-x", 8192)
	if got := p3.betaHeaderValue(true); got != "" {
		t.Fatalf("inactive betaHeaderValue = %q, want empty", got)
	}
	if opts := p3.betaHeaderOpts(true); len(opts) != 0 {
		t.Fatalf("inactive betaHeaderOpts = %d options, want 0", len(opts))
	}
}

// TestIssue2774RetryPathsKeepFullBetaSet pins Bug B at the source level:
// after the fix no call site may rebuild beta options from a subset (the
// thinking/effort retries used to drop the PTC token while params still
// declared the code_execution tool). The single aggregator is the only
// emitter of the anthropic-beta header key.
func TestIssue2774RetryPathsKeepFullBetaSet(t *testing.T) {
	src, err := readFileForIssue2774("anthropic.go")
	if err != nil {
		t.Fatalf("read anthropic.go: %v", err)
	}
	// Exactly one WithHeader("anthropic-beta", ...) emitter in the file.
	if n := strings.Count(src, `WithHeader("anthropic-beta"`); n != 1 {
		t.Fatalf("anthropic.go has %d WithHeader(\"anthropic-beta\") emitters, want exactly 1 (the aggregator) - split emitters overwrite each other per SDK Set semantics (#2774)", n)
	}
	// The retry paths must not nil out the option set that carries the
	// beta header (pre-fix: `callOpts = nil` before the thinking retry).
	if strings.Contains(src, "callOpts = nil") {
		t.Fatal("callOpts = nil found - retry paths must keep the full beta-header option set (#2774 Bug B)")
	}
}

// readFileForIssue2774 loads a source file from the package directory.
func readFileForIssue2774(name string) (string, error) {
	data, err := os.ReadFile(name)
	return string(data), err
}
