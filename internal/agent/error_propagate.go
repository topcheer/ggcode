package agent

// Error Propagation Chain Detection -- Degraded-Output Contamination Tracking
//
// Research basis: Openlayer "AI Agent Failure Modes: Tool-Calling Errors,
// Infinite Loops & Propagation" (July 2026) identifies silent failure
// propagation as the most damaging failure type in production agents:
//
//   "Silent Failures: Some tools return a success status code while
//    delivering empty or malformed payloads. Without explicit validation
//    on the return object, the agent treats a failed retrieval as a
//    successful one and builds subsequent reasoning on missing data."
//
//   "Error Propagation: In a multi-step agent, the output of one step
//    becomes the input to the next. If step two produces a malformed
//    result because step one retrieved the wrong context, step three
//    will reason over bad data without knowing it is bad."
//
//   "Propagation awareness means the agent checks its own outputs at
//    each step before passing them forward."
//
// The compound failure probability math (agentmarketcap, 2026): if per-step
// accuracy is 85% across 10 dependent steps, compound success is ~20%.
// Each silently-degraded intermediate step that is NOT verified degrades
// all downstream reasoning that depends on it.
//
// Gap in existing ggcode systems:
//   - error_cascade.go: tracks EXPLICIT errors (IsError=true) that share
//     a root resource. Does not detect degraded-but-not-errored outputs.
//   - tool_output_guard.go: detects oversized outputs on individual calls.
//     Does not track whether degraded outputs propagate downstream.
//   - silent_error_advancement.go: detects silent error acceptance but
//     does not track the propagation chain that follows.
//   - error_classifier.go: classifies error types. Does not detect
//     degraded-output → downstream-consumption chains.
//   - read_validity.go: validates read freshness. Does not detect
//     empty/degraded tool outputs being used as reasoning basis.
//
// This component fills the gap with deterministic, zero-LLM-cost detection:
//
//   1. DEGRADED OUTPUT DETECTION: identify tool results that returned
//      "successfully" (IsError=false) but contain suspiciously degraded
//      content -- empty, extremely short, null-like, or truncated patterns.
//
//   2. PROPAGATION TRACKING: when a degraded output is followed by 2+
//      subsequent tool calls (which likely build on the degraded data),
//      flag the propagation chain. The agent is reasoning on potentially
//      corrupted state without verifying the intermediate result.
//
//   3. GUIDANCE: inject guidance to verify or re-examine the degraded
//      intermediate output before continuing to build on it.

import (
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// propagateWarnSteps: minimum subsequent tool calls after a degraded
	// output to trigger propagation warning.
	propagateWarnSteps = 2

	// propagateMaxChains: maximum degraded chains tracked (memory bound).
	propagateMaxChains = 8

	// degradedMinContentLen: results shorter than this (after trimming)
	// that claim success are suspicious.
	degradedMinContentLen = 15

	// degradedMaxChainsWarn: maximum propagation warnings per run.
	degradedMaxChainsWarn = 2
)

// degradedKind classifies the type of degraded output detected.
type degradedKind int

const (
	degradedNone      degradedKind = iota
	degradedEmpty                  // completely empty content on a "successful" call
	degradedNullish                // null, nil, none, undefined, {} patterns
	degradedTruncated              // explicit truncation markers
	degradedNoResult               // "no results", "not found", "0 matches" on search
)

func (k degradedKind) String() string {
	switch k {
	case degradedEmpty:
		return "empty"
	case degradedNullish:
		return "nullish"
	case degradedTruncated:
		return "truncated"
	case degradedNoResult:
		return "no-result"
	default:
		return "none"
	}
}

// degradedOutput records a single degraded tool output.
type degradedOutput struct {
	toolName string
	kind     degradedKind
	step     int // sequence number when recorded
}

// propagationChain tracks a degraded output and how many subsequent
// tool calls have been issued since (potential downstream consumers).
type propagationChain struct {
	origin     degradedOutput
	downstream int // count of subsequent tool calls
	guided     bool
}

// errorPropagateState tracks degraded outputs and their downstream propagation.
type errorPropagateState struct {
	mu sync.Mutex

	// chains: active propagation chains (degraded outputs still within window).
	chains []*propagationChain

	// totalSteps: global step counter for this run.
	totalSteps int

	// warningsFired: how many propagation warnings emitted this run.
	warningsFired int
}

func newErrorPropagateState() *errorPropagateState {
	return &errorPropagateState{}
}

func (e *errorPropagateState) reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.chains = nil
	e.totalSteps = 0
	e.warningsFired = 0
}

// degradedPatterns are substrings that indicate a "successful" but
// degraded/empty result. These are matched case-insensitively.
var degradedPatterns = []string{
	"no results", "no matches", "0 matches", "0 results",
	"nothing found", "not found", "no such file", "file not found",
	"no elements", "empty result", "zero results",
	"no symbols", "no definitions", "no references",
}

// truncationMarkers indicate the tool output was truncated.
var truncationMarkers = []string{
	"... truncated", "[truncated]", "output truncated",
	"... results omitted", "[output too large",
}

// nullishValues are exact-match patterns for null/empty returns.
var nullishValues = []string{
	"null", "nil", "none", "undefined", "n/a", "{}", "[]",
}

// classifyDegraded checks whether a "successful" (non-error) tool result
// contains degraded content. Returns the kind and whether it's degraded.
func classifyDegraded(toolName, content string) degradedKind {
	trimmed := strings.TrimSpace(content)

	// Completely empty content on a successful call.
	if trimmed == "" {
		return degradedEmpty
	}

	lower := strings.ToLower(trimmed)

	// Exact nullish values (content is JUST one of these).
	if len(lower) <= 12 {
		for _, nv := range nullishValues {
			if lower == nv {
				return degradedNullish
			}
		}
	}

	// Truncation markers.
	for _, m := range truncationMarkers {
		if strings.Contains(lower, m) {
			return degradedTruncated
		}
	}

	// "No result" patterns for search/read tools.
	for _, p := range degradedPatterns {
		if strings.Contains(lower, p) {
			return degradedNoResult
		}
	}

	// Very short non-error output (suspicious for tools expected to
	// return substantive content). Only applies to read/search tools
	// that normally return larger outputs.
	if isContentTool(toolName) && len(trimmed) < degradedMinContentLen {
		return degradedEmpty
	}

	return degradedNone
}

// isContentTool returns true for tools that normally return substantial content.
func isContentTool(toolName string) bool {
	switch toolName {
	case "read_file", "multi_file_read", "grep", "search_files", "code_search",
		"lsp_references", "lsp_definition", "lsp_workspace_symbols",
		"lsp_implementation", "web_search", "web_fetch", "glob":
		return true
	default:
		return false
	}
}

// recordResult is called after every tool execution. It first advances
// existing chains, then checks if this result is a new degraded output.
// Returns guidance text if propagation warning should be injected.
func (e *errorPropagateState) recordResult(toolName, content string, isError bool) string {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.totalSteps++

	// Advance all existing chains (this call is a potential downstream consumer).
	for _, c := range e.chains {
		if c.guided {
			continue
		}
		c.downstream++
	}

	// Check if this result is a new degraded output.
	var guidance string
	if !isError {
		kind := classifyDegraded(toolName, content)
		if kind != degradedNone {
			// Start a new propagation chain.
			if len(e.chains) < propagateMaxChains {
				e.chains = append(e.chains, &propagationChain{
					origin: degradedOutput{
						toolName: toolName,
						kind:     kind,
						step:     e.totalSteps,
					},
				})
			}
		}
	}

	// Check if any unguided chain has reached the propagation threshold.
	for _, c := range e.chains {
		if c.guided || c.downstream < propagateWarnSteps {
			continue
		}
		if e.warningsFired >= degradedMaxChainsWarn {
			break
		}
		c.guided = true
		e.warningsFired++
		guidance = e.formatPropagationGuidance(c)
		break // one warning per call
	}

	// Trim guided chains that are old enough (keep memory bounded).
	if len(e.chains) > propagateMaxChains {
		e.chains = e.chains[len(e.chains)-propagateMaxChains:]
	}

	if guidance != "" {
		debug.Log("agent", "error_propagate: propagation chain detected (warning %d/%d)", e.warningsFired, degradedMaxChainsWarn)
	}

	return guidance
}

func (e *errorPropagateState) formatPropagationGuidance(c *propagationChain) string {
	return fmt.Sprintf(
		"[Error Propagation Chain] A prior tool call (%s) returned a degraded "+
			"%s result that was NOT flagged as an error, yet %d subsequent tool "+
			"calls have built on it. Reasoning derived from empty/truncated/null "+
			"intermediate outputs compounds silently -- each downstream step "+
			"inherits the corrupted state. Before continuing, verify whether the "+
			"%s output from %s was actually valid. If it was genuinely empty "+
			"(e.g., no search results), confirm your approach is still grounded "+
			"rather than building conclusions on missing data.",
		c.origin.toolName, c.origin.kind.String(),
		c.downstream, c.origin.kind.String(), c.origin.toolName,
	)
}
