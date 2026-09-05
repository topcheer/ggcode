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

	// degradedPatternMaxLen: "no result" substring patterns are only
	// checked on outputs at most this long (after trimming). This
	// prevents false positives where legitimate code content returned
	// by grep/read_file contains phrases like "not found" in error-
	// handling code (e.g., fmt.Errorf("record not found")).
	degradedPatternMaxLen = 50

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

// truncationFooterPrefixes are line-anchored prefixes of wrapper/footer
// lines that tools THEMSELVES append when they truncate their output
// (e.g. read_file appends "[File truncated: showing lines 1-10 of 50. ...]").
// Matching is per-line (line-start anchored), NOT a full-content substring
// match: file content legitimately containing the literal text
// "output truncated" (e.g. this detector's own source, or truncation code
// in other tools) must not be classified as a degraded read.
var truncationFooterPrefixes = []string{
	"[file truncated:", "[showing lines", "[output too large",
	"[truncated:", "... [output truncated]", "... results omitted",
	// #1554-C: the ACTUAL prefix guardToolOutput writes (the list's
	// '... [output truncated]' literal never matched it - head+tail
	// truncations were undetectable). Shared constant, no re-drift.
	toolHeadTailTruncationPrefix,
}

// hasTruncationFooter reports whether any line of the output is a
// tool-appended truncation footer line.
func hasTruncationFooter(content string) bool {
	for _, ln := range strings.Split(content, "\n") {
		t := strings.TrimSpace(strings.ToLower(ln))
		for _, p := range truncationFooterPrefixes {
			if strings.HasPrefix(t, p) {
				return true
			}
		}
	}
	return false
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

	// Truncation: only tool-appended footer lines (line-anchored), never
	// a full-content substring match (see truncationFooterPrefixes).
	// #1457-A: footers that CONTINUE-PAGINATION GUIDANCE (offset/limit,
	// "Use read_file with offset/limit") are the tool's designed paging
	// signal - normal large-file reads chained as 'degraded truncated'
	// and burned the 2-per-run budget on noise (live false positives
	// observed mid-review; the comment above documents the footer yet
	// still classified it degraded). Only footers WITHOUT guidance (raw
	// "[output too large" / "[truncated:") stay degraded - those signal
	// lost content with no continuation path.
	if hasTruncationFooter(trimmed) && !hasPaginationGuidance(trimmed) {
		return degradedTruncated
	}

	// "No result" patterns — only check on SHORT outputs to avoid
	// false positives where legitimate code content contains these
	// phrases (e.g., error-handling code with "not found").
	if len(trimmed) <= degradedPatternMaxLen {
		for _, p := range degradedPatterns {
			if strings.Contains(lower, p) {
				return degradedNoResult
			}
		}
	}

	// Very short non-error output (suspicious for tools expected to
	// return substantive content). Only applies to read/search tools
	// that normally return larger outputs. Path-list tools (glob, and
	// grep in files_with_matches mode) are exempt: a single short file
	// name like "a.go" is a completely successful minimal result.
	if isContentTool(toolName) && !isPathListTool(toolName) && len(trimmed) < degradedMinContentLen {
		return degradedEmpty
	}

	return degradedNone
}

// isPathListTool returns true for tools whose minimal successful output
// is naturally short (a list of file paths). These are exempt from the
// degradedMinContentLen heuristic.
func isPathListTool(toolName string) bool {
	switch toolName {
	case "glob", "grep": // grep defaults to files_with_matches mode (path list)
		return true
	default:
		return false
	}
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
		"[Error Propagation Chain] A prior tool call (%s, step %d) returned a "+
			"degraded %s result that was NOT flagged as an error, yet %d "+
			"subsequent tool calls may have consumed it. There is no data-flow "+
			"tracking, so these later calls may be unrelated -- but if any of "+
			"them built on the %s output, reasoning derived from empty/truncated/null "+
			"intermediate outputs compounds silently. Before continuing, verify "+
			"whether the %s output from %s (step %d) was actually valid. If it was "+
			"genuinely empty (e.g., no search results), confirm your approach is "+
			"still grounded rather than building conclusions on missing data.",
		c.origin.toolName, c.origin.step, c.origin.kind.String(),
		c.downstream, c.origin.kind.String(),
		c.origin.kind.String(), c.origin.toolName, c.origin.step,
	)
}

// hasPaginationGuidance reports whether the content contains an explicit
// continuation pointer the tool itself appended (#1457-A) - "use ... with
// offset/limit", "use read_command_output", pagination hints. Such
// footers are the tool's DESIGNED paging signal, not degradation.
func hasPaginationGuidance(content string) bool {
	// #1554-A: the truncation FOOTER lives in the last lines, but this
	// check scanned the WHOLE content - any body mention of "offset/limit"
	// (pagination hint strings embedded in THIS repo's own output/format
	// code) exempted a genuinely truncated result. Anchor to the tail the
	// footer detector itself uses.
	lines := strings.Split(content, "\n")
	if len(lines) > 5 {
		lines = lines[len(lines)-5:]
	}
	tail := strings.ToLower(strings.Join(lines, "\n"))
	return strings.Contains(tail, "offset/limit") ||
		strings.Contains(tail, "offset and limit") ||
		strings.Contains(tail, "use read_command_output") ||
		strings.Contains(tail, "use wait_command")
}
