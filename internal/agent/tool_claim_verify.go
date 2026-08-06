package agent

// Tool Output Claim Verification
//
// Research basis: AgentRx (arXiv:2602.02475, Microsoft Research, Feb 2026)
// identifies "Misinterpretation of Tool Output" as one of 9 cross-domain agent
// failure categories. Agents read tool outputs incorrectly - claiming success
// when commands failed, claiming findings when searches returned nothing, or
// acting on stale "not found" messages. The paper's key insight: failure is
// often NOT in the tool result itself (IsError=true), but in the agent's
// interpretation of a nominally-successful result.
//
// This detector scans tool results for commonly misread failure signals that
// IsError does NOT capture:
//
//   - Commands that exit 0 but print errors (exit code in output, panics)
//   - "No such file" / "does not exist" / "not found" in results
//   - "0 matches" / "no results" in search output
//   - Build/test output containing "FAIL" or "panic:" despite exit 0
//
// When detected, it injects a brief, specific verification reminder appended
// to the tool result content. This ensures the LLM correctly interprets the
// result before making claims in its next response.
//
// Design: zero-LLM-cost, deterministic, non-blocking, capped at 3 injections
// per run to avoid noise.

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/topcheer/ggcode/internal/debug"
)

// claimVerifyState tracks verification nudge injections for the current run.
type claimVerifyState struct {
	injections int
}

func newClaimVerifyState() *claimVerifyState {
	return &claimVerifyState{}
}

func (c *claimVerifyState) reset() {
	c.injections = 0
}

const claimVerifyMaxInjections = 3

// toolsForClaimVerify are tools whose results most commonly get misinterpreted.
var toolsForClaimVerify = map[string]bool{
	"run_command":     true,
	"start_command":   true,
	"grep":            true,
	"search_files":    true,
	"glob":            true,
	"read_file":       true,
	"multi_file_read": true,
	"code_search":     true,
	"lsp_definition":  true,
	"lsp_references":  true,
	"lsp_symbols":     true,
	"lsp_hover":       true,
	"lsp_diagnostics": true,
}

// claimVerifyPatterns are (pattern, message) pairs. Patterns are matched
// case-insensitively against the tool result content. Each pattern targets
// a specific misinterpretation risk identified in the AgentRx taxonomy.
type claimVerifyPattern struct {
	pattern string
	msg     string
}

var claimVerifyPatterns = []claimVerifyPattern{
	// Exit code failure masked in non-error output
	{"exit code: 1", "Command exited with code 1 (failure). Do not claim this command succeeded."},
	{"exit code 1", "Command exited with code 1 (failure). Do not claim this command succeeded."},
	{"exit status 1", "Command exited with status 1 (failure). Do not claim this command succeeded."},
	{"exit=1", "Command exited with code 1 (failure). Do not claim this command succeeded."},
	// Runtime crashes
	{"panic:", "Output contains a Go panic. This indicates a crash - do not claim success."},
	{"fatal error:", "Output contains a fatal error. Do not claim this operation succeeded."},
	// Build/test failures
	{"build failed", "Build failed. Do not claim the build passed."},
	{"compilation failed", "Compilation failed. Do not claim compilation succeeded."},
	{"FAIL:", "Test output contains FAIL. Do not claim all tests passed."},
	{"FAIL\t", "Test output contains FAIL. Do not claim all tests passed."},
	// File not found
	{"no such file or directory", "File/path does not exist. Do not claim you accessed it."},
	{"does not exist", "Path does not exist. Do not claim you found it."},
	{"not found in", "Item not found. Do not claim it exists."},
	// Empty search results
	{"0 matches", "Search returned 0 matches. Do not claim results were found."},
	{"no matches found", "Search returned no matches. Do not claim results were found."},
	{"no results", "Search returned no results. Do not claim something was found."},
}

// checkClaimVerify scans a tool result for commonly misinterpreted failure
// signals and returns guidance if a risk is detected. Returns "" if no issue
// or if the injection cap has been reached.
func (c *claimVerifyState) check(toolName, content string, isError bool) string {
	if c.injections >= claimVerifyMaxInjections {
		return ""
	}
	if !toolsForClaimVerify[toolName] {
		return ""
	}
	if content == "" {
		return ""
	}

	// If the result is already flagged as an error, the agent is more likely
	// to interpret it correctly — skip to reduce noise.
	if isError {
		return ""
	}

	lower := strings.ToLower(content)
	// Only scan the first 4000 chars to keep cost low on huge outputs.
	if len(lower) > 4000 {
		lower = lower[:4000]
	}

	for _, cvp := range claimVerifyPatterns {
		if strings.Contains(lower, cvp.pattern) {
			c.injections++
			debug.Log("claim_verify", "misinterpretation risk detected: tool=%s pattern=%q", toolName, cvp.pattern)
			return fmt.Sprintf("[Verify] %s Re-read the tool output carefully before proceeding.", cvp.msg)
		}
	}

	return ""
}

// trimNonPrint removes trailing non-printable noise for cleaner pattern matching.
func trimNonPrint(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		return !unicode.IsPrint(r)
	})
}
