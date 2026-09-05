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
// response, or act on them? If a tool returned "the function is defined in
// auth.go:42" and the agent's next action doesn't touch auth.go or mention
// it, that's an integration gap.
//
// Integration is recognized two ways (issue #345 fix -- "doing" over
// "saying"):
//
//  1. Action evidence: a subsequent edit_file/write_file whose target path
//     (present in the tool result content) hits a pending evidence path is
//     the strongest form of integration. The standard agent flow is
//     grep -> edit_file -> summarize without echoing full paths; that flow
//     must NOT be flagged.
//  2. Relaxed text evidence: mentioning the file's base name
//     (filepath.Base) or package (directory) name counts -- full-path
//     substrings are not required.
//
// Additional anti-false-positive rules:
//   - Pending evidence accumulates (append + dedup, capped) across
//     read_file -> grep -> read_file sequences instead of overwriting.
//   - Browsing outputs that are pure path lists (e.g. grep
//     files_with_matches) are navigation aids, not evidence that must be
//     echoed back.
//   - Pending evidence does not survive past a mutating tool call
//     (edit/write/run_command): whether or not the mutation consumed it,
//     stale evidence must not fire against later summaries.
//
// Design: zero-LLM-cost, deterministic, non-blocking. Fires at most 2
// warnings per run to avoid noise.

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// integrationState tracks tool output integration monitoring for the current run.
type integrationState struct {
	warnings int
	// pendingEvidence holds high-signal tokens extracted from recent
	// information tool results. Appended (deduped) across consecutive
	// retrieval calls; consumed by checkIntegration or by mutating tools.
	pendingEvidence []string
	// pendingTool tracks which tool most recently produced pending evidence.
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

// integrationMaxPendingEvidence caps accumulated pending evidence tokens.
const integrationMaxPendingEvidence = 8

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

// integrationMutatingTools are tools that change state. Any such call ends
// the "investigation phase": evidence consumed by the mutation's target is
// integrated by action, and unrelated evidence expires rather than
// surviving across intervening mutating calls (penetration limit).
// Derived from the canonical sourceMutatingTools superset plus command/git
// side-effect tools (#738).
var integrationMutatingTools = derivedEditTools(map[string]bool{
	"run_command":   true,
	"start_command": true,
	"git_add":       true,
	"git_commit":    true,
	"git_reset":     true,
	"git_revert":    true,
	"git_checkout":  true,
	"git_stash":     true,
	"git_tag":       true,
})

// Patterns for extracting high-signal tokens from tool outputs.
// These represent the "evidence" an agent should carry forward.
var (
	integrationFilePathRe = regexp.MustCompile(`(?:[\w.-]+/)+[\w.-]+\.\w+`)
	integrationLineRefRe  = regexp.MustCompile(`(?i)(?:line|l)[:\s]+(\d{1,4})`)
	integrationGoSymbolRe = regexp.MustCompile(`\b(?:func|type|var|const)\s+([A-Z]\w+)`)
	// integrationPathLineRe matches a single line that is exactly a
	// path-like token (no spaces, contains /, has a short extension) --
	// used to detect pure path-list browsing outputs.
	integrationPathLineRe = regexp.MustCompile(`^[\w./~-]+\.[A-Za-z]{1,6}$`)
)

// minEvidenceLen is the minimum evidence token length to avoid matching
// trivially short strings like "go" or "a/b".
const minEvidenceLen = 4

// integrationDirNoise filters directory names too generic to count as
// package-name integration (they appear in almost every assistant text).
var integrationDirNoise = map[string]bool{
	"internal": true, "agent": true, "pkg": true, "src": true,
	"test": true, "tests": true, "vendor": true, "example": true,
	"examples": true, "main": true, "app": true, "lib": true,
	"util": true, "utils": true, "common": true, "core": true,
	"package": true, "content": true, "docs": true, "site": true,
}

// recordToolEvidence processes a tool result for integration tracking.
// Called after each tool execution:
//   - mutating tools consume/expire pending evidence (action integration),
//   - information tools append extracted high-signal tokens as evidence.
func (s *integrationState) recordToolEvidence(toolName, content string) {
	if integrationMutatingTools[toolName] {
		s.consumeEvidenceOnMutation(toolName, content)
		return
	}
	if !toolsForIntegration[toolName] || content == "" {
		return
	}

	// Pure path-list browsing output (e.g. grep files_with_matches) is a
	// navigation aid, not evidence the summary must echo back.
	if isPathListOutput(content) {
		return
	}

	tokens := extractEvidenceTokens(content)
	if len(tokens) == 0 {
		return
	}

	s.appendEvidence(tokens)
	s.pendingTool = toolName
}

// extractEvidenceTokens pulls high-signal tokens (file paths, line refs,
// exported Go symbols) from a tool result, deduped and noise-filtered.
func extractEvidenceTokens(content string) []string {
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
	return tokens
}

// appendEvidence appends new evidence tokens to pendingEvidence with dedup,
// capping the total so stale tokens cannot accumulate unboundedly. This
// keeps read_file -> grep -> read_file sequences' evidence instead of
// overwriting.
func (s *integrationState) appendEvidence(tokens []string) {
	for _, tok := range tokens {
		dup := false
		for _, existing := range s.pendingEvidence {
			if existing == tok {
				dup = true
				break
			}
		}
		if !dup {
			s.pendingEvidence = append(s.pendingEvidence, tok)
		}
	}
	if len(s.pendingEvidence) > integrationMaxPendingEvidence {
		s.pendingEvidence = s.pendingEvidence[len(s.pendingEvidence)-integrationMaxPendingEvidence:]
	}
}

// consumeEvidenceOnMutation handles a mutating tool result. If the tool's
// target path (extracted from the result content, e.g. edit_file's
// "Replaced 1 occurrence in <path>:" / "[Changes]" output) hits a pending
// evidence path, that evidence was integrated by action. Regardless, no
// pending evidence survives past a mutating call -- stale evidence must not
// fire against later summaries.
func (s *integrationState) consumeEvidenceOnMutation(toolName, content string) {
	if len(s.pendingEvidence) == 0 {
		return
	}

	scan := content
	if len(scan) > 2000 {
		scan = scan[:2000]
	}
	targets := integrationFilePathRe.FindAllString(scan, 5)

	consumed := 0
	for _, ev := range s.pendingEvidence {
		if !strings.Contains(ev, "/") {
			continue // only path-shaped evidence can be matched by target paths
		}
		evBase := filepath.Base(ev)
		for _, t := range targets {
			if strings.EqualFold(filepath.Base(t), evBase) {
				consumed++
				break
			}
		}
	}
	if consumed > 0 {
		debug.Log("integration_monitor", "evidence integrated by action: tool=%s consumed=%d", toolName, consumed)
	}

	s.pendingEvidence = nil
	s.pendingTool = ""
}

// isPathListOutput reports whether content is a multi-line list of pure
// path tokens (one path per line, no spaces, contains /, file extension).
// Such browsing output (grep files_with_matches) is navigation, not
// evidence.
func isPathListOutput(content string) bool {
	scan := content
	if len(scan) > 3000 {
		scan = scan[:3000]
	}
	lines := strings.Split(strings.TrimRight(scan, "\n"), "\n")
	paths := 0
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if strings.ContainsAny(l, " \t") {
			return false
		}
		if !strings.Contains(l, "/") {
			return false
		}
		if !integrationPathLineRe.MatchString(l) {
			return false
		}
		paths++
	}
	return paths >= 2
}

// checkIntegration examines assistant text for references to pending
// evidence tokens. Matching is relaxed (issue #345): the file's base name
// (filepath.Base) or package (directory) name counts as integration -- the
// full path substring is not required. Returns guidance if evidence was
// recorded but nothing was referenced. Called after assistant text is
// captured.
func (s *integrationState) checkIntegration(assistantText string) string {
	if len(s.pendingEvidence) == 0 || s.warnings >= integrationMaxWarnings {
		s.pendingEvidence = nil
		s.pendingTool = ""
		return ""
	}

	evidence := s.pendingEvidence
	toolName := s.pendingTool

	// #1508: check the empty-text bail BEFORE clearing pending evidence.
	// Text-less iterations (pure tool-call turns - the common agentic
	// output mode) used to evaporate the recorded evidence unexamined,
	// defeating the keep-evidence design the comment below describes.
	if assistantText == "" {
		return ""
	}

	// Clear pending regardless of outcome.
	s.pendingEvidence = nil
	s.pendingTool = ""

	lower := strings.ToLower(assistantText)
	integrated := 0
	for _, tok := range evidence {
		if evidenceTokenInText(tok, lower) {
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

// evidenceTokenInText reports whether an evidence token is referenced in
// (lowercased) assistant text. Full-token substring counts; for path-shaped
// tokens, a base-name or package-name mention also counts.
func evidenceTokenInText(tok, lowerText string) bool {
	if strings.Contains(lowerText, tok) {
		return true
	}
	if !strings.Contains(tok, "/") {
		return false // symbol/line-ref tokens: exact substring only
	}
	base := filepath.Base(tok)
	if len(base) >= minEvidenceLen && !isCommonNoise(base) && strings.Contains(lowerText, base) {
		return true
	}
	// Package name: the last directory segment (e.g. "internal/tool" -> "tool").
	dir := filepath.Dir(tok)
	if dir != "." && dir != "/" && !strings.HasSuffix(dir, "\\") {
		pkg := filepath.Base(dir)
		if len(pkg) >= minEvidenceLen && !integrationDirNoise[pkg] && !isCommonNoise(pkg) && strings.Contains(lowerText, pkg) {
			return true
		}
	}
	return false
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

// --- Agent integration ---

// integrationRecordToolResult extracts evidence tokens from an information
// tool result. Called in the tool result loop after each tool execution.
func (a *Agent) integrationRecordToolResult(toolName, content string) {
	if a.integrationMonitor == nil {
		return
	}
	a.integrationMonitor.recordToolEvidence(toolName, content)
}

// integrationCheckAndWarn checks the assistant text against pending evidence
// and injects guidance if the evidence was not integrated. Called after the
// assistant text is captured in the agent loop.
func (a *Agent) integrationCheckAndWarn(assistantText string) {
	if a.integrationMonitor == nil {
		return
	}
	if hint := a.integrationMonitor.checkIntegration(assistantText); hint != "" {
		debug.Log("integration_monitor", "guidance injected: evidence not integrated")
		a.injectGuidance(hint)
	}
}

// integrationResetForRun clears integration state for a new run.
func (a *Agent) integrationResetForRun() {
	if a.integrationMonitor != nil {
		a.integrationMonitor.reset()
	}
}
