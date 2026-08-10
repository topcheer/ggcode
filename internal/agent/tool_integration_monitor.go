package agent

// Tool Output Integration Monitor (Cross-Step Evidence Accumulation)
//
// Research basis: TRACE framework (arXiv:2606.07054) introduces "Adaptive
// Cross-Step Evidence" tracking for long-horizon LLM agent trajectories.
// The key insight: agents often call information-retrieval tools (search,
// grep, read_file, diagnostics) whose outputs contain actionable signals
// (file paths, symbol names, error messages), but then fail to integrate
// that evidence into their subsequent reasoning. The output is read but
// not *used* -- the agent proceeds as if it never saw the result.
//
// Unlike tool_claim_verify (which checks if the agent misinterprets failure
// signals), this detector checks the *positive* direction: did the agent
// actually reference key tokens from the tool output in its next text
// response? If a tool returned "the function is defined in auth.go:42" and
// the agent's next action doesn't touch auth.go or mention line 42, that's
// an integration gap.
//
// Also unlike tool_result_redundancy (which detects re-reading the same
// content), this checks whether the *first* read was acted upon.
//
// Design: zero-LLM-cost, deterministic, non-blocking. Extracts high-signal
// tokens from tool outputs (file paths, line numbers, symbol names) and
// checks if subsequent assistant text references them. Fires at most 2
// warnings per run to avoid noise.

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// integrationState tracks tool output integration monitoring for the current run.
type integrationState struct {
	warnings int
	// pendingEvidence holds high-signal tokens extracted from the most recent
	// information tool result. Cleared after checking against assistant text.
	pendingEvidence []string
	// pendingTool tracks which tool produced the pending evidence.
	pendingTool string
}

func newIntegrationState() *integrationState {
	return &integrationState{}
}

func (s *integrationState) reset() {
	s.warnings = 0
	s.pendingEvidence = nil
	s.pendingTool = ""
}

const integrationMaxWarnings = 2

// toolsForIntegration are information-retrieval tools whose outputs contain
// actionable signals the agent should integrate into subsequent reasoning.
var toolsForIntegration = map[string]bool{
	"read_file":       true,
	"multi_file_read": true,
	"grep":            true,
	"search_files":    true,
	"code_search":     true,
	"lsp_definition":  true,
	"lsp_references":  true,
	"lsp_symbols":     true,
	"lsp_hover":       true,
	"lsp_diagnostics": true,
	"git_show":        true,
	"git_log":         true,
	"git_diff":        true,
	"git_blame":       true,
	"web_search":      true,
	"web_fetch":       true,
}

// Patterns for extracting high-signal tokens from tool outputs.
// These represent the "evidence" an agent should carry forward.
var (
	integrationFilePathRe = regexp.MustCompile(`(?:[\w.-]+/)+[\w.-]+\.\w+`)
	integrationLineRefRe  = regexp.MustCompile(`(?i)(?:line|l)[:\s]+(\d{1,4})`)
	integrationGoSymbolRe = regexp.MustCompile(`\b(?:func|type|var|const)\s+([A-Z]\w+)`)
)

// minEvidenceLen is the minimum evidence token length to avoid matching
// trivially short strings like "go" or "a/b".
const minEvidenceLen = 4

// recordToolEvidence extracts high-signal tokens from a tool result and
// stores them as pending evidence. Called after each tool execution.
func (s *integrationState) recordToolEvidence(toolName, content string) {
	if !toolsForIntegration[toolName] || content == "" {
		return
	}

	// Only capture from first 3000 chars to keep cost low.
	scan := content
	if len(scan) > 3000 {
		scan = scan[:3000]
	}

	var tokens []string
	seen := make(map[string]bool)

	extract := func(re *regexp.Regexp, group int) {
		matches := re.FindAllStringSubmatch(scan, 10)
		for _, m := range matches {
			var tok string
			if group < len(m) {
				tok = m[group]
			}
			tok = strings.TrimSpace(strings.ToLower(tok))
			if len(tok) >= minEvidenceLen && !seen[tok] && !isCommonNoise(tok) {
				seen[tok] = true
				tokens = append(tokens, tok)
			}
		}
	}

	extract(integrationFilePathRe, 0)
	extract(integrationLineRefRe, 0) // full match: "line 142" - more meaningful than bare number
	extract(integrationGoSymbolRe, 1)

	if len(tokens) >= 2 {
		s.pendingEvidence = tokens
		s.pendingTool = toolName
	}
}

// checkIntegration examines assistant text for references to pending evidence
// tokens. If evidence was recorded but none appears in the text, it returns
// guidance. Called after assistant text is captured.
func (s *integrationState) checkIntegration(assistantText string) string {
	if len(s.pendingEvidence) == 0 || s.warnings >= integrationMaxWarnings {
		s.pendingEvidence = nil
		s.pendingTool = ""
		return ""
	}

	evidence := s.pendingEvidence
	toolName := s.pendingTool

	// Clear pending regardless of outcome.
	s.pendingEvidence = nil
	s.pendingTool = ""

	if assistantText == "" {
		return ""
	}

	lower := strings.ToLower(assistantText)
	integrated := 0
	for _, tok := range evidence {
		if strings.Contains(lower, tok) {
			integrated++
		}
	}

	// If the agent referenced fewer than 30% of evidence tokens, flag it.
	threshold := len(evidence) / 3
	if threshold < 1 {
		threshold = 1
	}

	if integrated >= threshold {
		return "" // Good integration.
	}

	s.warnings++
	debug.Log("integration_monitor", "evidence not integrated: tool=%s tokens=%d integrated=%d", toolName, len(evidence), integrated)

	// Build a short example of what was missed.
	example := evidence[0]
	if len(evidence) > 1 {
		example = evidence[0] + ", " + evidence[1]
	}

	return fmt.Sprintf(
		"[evidence] Previous %s returned key info (%s) not in your reasoning. Integrate findings before proceeding.",
		toolName, example,
	)
}

// isCommonNoise filters out tokens that are too generic to constitute
// actionable evidence (common paths, language keywords, etc.).
func isCommonNoise(tok string) bool {
	noise := map[string]bool{
		"main.go":     true,
		"go.mod":      true,
		"go.sum":      true,
		"makefile":    true,
		"readme.md":   true,
		"readme":      true,
		"package":     true,
		"import":      true,
		"function":    true,
		"error":       true,
		"string":      true,
		"table":       true,
		"true":        true,
		"false":       true,
		"none":        true,
		"null":        true,
		"type":        true,
		"value":       true,
		"return":      true,
		"foo":         true,
		"bar":         true,
		"test":        true,
		"example.com": true,
		"http":        true,
		"https":       true,
	}
	return noise[tok]
}
