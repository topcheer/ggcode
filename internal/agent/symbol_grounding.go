package agent

// Symbol Grounding Verifier
//
// Research basis:
//   - Farquhar et al. (2024): "Detecting Hallucinations in Large Language
//     Models using Semantic Entropy" -- shows LLMs confidently hallucinate
//     when they lack grounded verification.
//   - Semantic Energy (arXiv:2508.14496, 2025): extends semantic entropy
//     to detect ungrounded model outputs beyond simple token-level entropy.
//   - Symbol Grounding Problem in LLMs (2025): LLMs reference symbols
//     (function names, file paths, type names) they never verified through
//     tool calls, leading to edit failures and wrong-file changes.
//
// Problem: AI coding agents reference code symbols in reasoning text --
// function names, file paths, type names -- that were never found or verified
// via tool calls (read_file, grep, lsp_*, search_files). These ungrounded
// symbol references lead to:
//
//  1. Edit failures: "update the processPayment function in service/payment.go"
//     -- but that function or file doesn't exist
//  2. Hallucinated API calls: "The getUserProfile method accepts..."
//  3. Wrong-file edits: modifying a file based on assumed content
//  4. Phantom debugging: "The bug is in authMiddleware" -- never verified
//
// Existing ggcode detectors RELATED but NOT covering this:
//   - tool_claim_verify.go: checks claimed tool actions, not symbol grounding
//   - read_validity.go: checks stale reads, not if symbols were ever read
//   - assumption_track.go: checks hedging language, not specific symbol refs
//
// Gap: No detector tracks whether code symbols mentioned in assistant text
// were discovered through tool calls. This detector addresses that.
//
// Design:
//   - Maintains a set of "grounded symbols" from tool call I/O
//   - Scans assistant text for symbol-like tokens near action verbs
//   - When 3+ ungrounded symbols found, injects grounding reminder
//   - Zero LLM cost -- pure deterministic pattern matching
//   - Fires at most 2 times per run

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	groundingWarnThreshold = 3
	groundingMaxWarns      = 2
	groundingMaxTracked    = 500
)

type symbolGroundingState struct {
	grounded  map[string]bool
	warnCount int
}

func newSymbolGroundingState() *symbolGroundingState {
	return &symbolGroundingState{grounded: make(map[string]bool)}
}

func (s *symbolGroundingState) reset() {
	s.grounded = make(map[string]bool)
	s.warnCount = 0
}

// recordGrounding extracts and stores grounded symbols from tool I/O.
func (s *symbolGroundingState) recordGrounding(toolInput, toolResult string) {
	if s.grounded == nil {
		s.grounded = make(map[string]bool)
	}
	for _, path := range extractGroundedPaths(toolInput) {
		s.addGrounded(path)
		if idx := strings.LastIndex(path, "/"); idx >= 0 {
			s.addGrounded(path[idx+1:])
		}
	}
	for _, ident := range extractGroundedIdents(toolResult) {
		s.addGrounded(ident)
	}
}

func (s *symbolGroundingState) addGrounded(sym string) {
	sym = strings.TrimSpace(sym)
	if sym == "" || len(sym) < 3 {
		return
	}
	if s.grounded == nil {
		s.grounded = make(map[string]bool)
	}
	if len(s.grounded) >= groundingMaxTracked {
		return
	}
	s.grounded[strings.ToLower(sym)] = true
}

func (s *symbolGroundingState) isGrounded(sym string) bool {
	if s.grounded == nil {
		return false
	}
	sym = strings.TrimSpace(sym)
	return sym != "" && s.grounded[strings.ToLower(sym)]
}

var (
	groundingFilePathRe = regexp.MustCompile(`(?:"(?:path|file_path|file|directory|source|target)"\s*:\s*"([^"]+)"|/"([^"]+\.\w+)")`)
	groundingFuncDefRe  = regexp.MustCompile(`(?:func|def|class|type|struct|interface|enum)\s+(\w+)`)
	// Backtick-quoted symbols in tool results -- built via concatenation
	// since raw string literals can't contain backtick characters.
	groundingResultBacktickRe = regexp.MustCompile("`" + `([A-Za-z_][A-Za-z0-9_.]+)` + "`")
	groundingDotSymRe         = regexp.MustCompile(`\b([a-z]\w*\.[A-Z]\w+)\b`)
)

func extractGroundedPaths(input string) []string {
	var paths []string
	for _, m := range groundingFilePathRe.FindAllStringSubmatch(input, -1) {
		for _, g := range m[1:] {
			if g != "" {
				paths = append(paths, g)
			}
		}
	}
	return paths
}

func extractGroundedIdents(result string) []string {
	seen := make(map[string]bool)
	var idents []string
	for _, m := range groundingFuncDefRe.FindAllStringSubmatch(result, -1) {
		if name := m[1]; !seen[name] {
			seen[name] = true
			idents = append(idents, name)
		}
	}
	for _, m := range groundingResultBacktickRe.FindAllStringSubmatch(result, -1) {
		if name := m[1]; !seen[name] && isPlausibleSymbol(name) {
			seen[name] = true
			idents = append(idents, name)
		}
	}
	return idents
}

func isPlausibleSymbol(s string) bool {
	if len(s) < 3 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			return true
		}
	}
	return false
}

// --- Assistant text scanning ---

var (
	groundingActionRe = regexp.MustCompile(`(?i)(?:edit|update|modify|fix|change|call|invoke|use|implement|add|remove|delete|replace|patch|refactor|debug)\b`)
	// Backtick-quoted symbols in assistant text
	groundingTextBacktickRe = regexp.MustCompile("`" + `([A-Za-z_][A-Za-z0-9_./-]+)` + "`")
	groundingTextCamelRe    = regexp.MustCompile(`\b([A-Z][a-z]+[A-Z]\w+)\b`)
	groundingTextPathRe     = regexp.MustCompile(`\b([\w-]+/[\w-]+(?:\.\w+)?)\b`)
	// Dot-qualified symbols in backticks
	groundingTextDotRe = regexp.MustCompile("`" + `([a-z]\w*\.[A-Z]\w+)` + "`")
)

func (a *Agent) maybeWarnGrounding(assistantText string, iteration int) string {
	s := a.symbolGrounding
	if s == nil || s.warnCount >= groundingMaxWarns {
		return ""
	}
	if !groundingActionRe.MatchString(assistantText) {
		return ""
	}

	candidates := make(map[string]bool)

	for _, m := range groundingTextBacktickRe.FindAllStringSubmatch(assistantText, -1) {
		if sym := m[1]; isPlausibleSymbol(sym) && !s.isGrounded(sym) {
			candidates[sym] = true
		}
	}
	for _, m := range groundingTextDotRe.FindAllStringSubmatch(assistantText, -1) {
		if sym := m[1]; !s.isGrounded(sym) {
			candidates[sym] = true
		}
	}

	camelIndices := groundingTextCamelRe.FindAllStringIndex(assistantText, -1)
	for idx, m := range groundingTextCamelRe.FindAllStringSubmatch(assistantText, -1) {
		sym := m[1]
		if !isPlausibleSymbol(sym) || s.isGrounded(sym) {
			continue
		}
		if idx < len(camelIndices) {
			pos := camelIndices[idx][0]
			start := pos - 80
			if start < 0 {
				start = 0
			}
			end := pos + len(sym) + 80
			if end > len(assistantText) {
				end = len(assistantText)
			}
			if groundingActionRe.MatchString(assistantText[start:end]) {
				candidates[sym] = true
			}
		}
	}

	for _, m := range groundingTextPathRe.FindAllStringSubmatch(assistantText, -1) {
		path := m[1]
		if s.isGrounded(path) || isCommonPath(path) {
			continue
		}
		baseName := path
		if idx := strings.LastIndex(path, "/"); idx >= 0 {
			baseName = path[idx+1:]
		}
		if !s.isGrounded(baseName) {
			candidates[path] = true
		}
	}

	if len(candidates) < groundingWarnThreshold {
		return ""
	}

	s.warnCount++

	var samples []string
	count := 0
	for sym := range candidates {
		samples = append(samples, sym)
		count++
		if count >= 5 {
			break
		}
	}

	debug.Log("agent", "Iteration %d: symbol grounding verifier detected %d ungrounded symbols (sample: %s)",
		iteration+1, len(candidates), strings.Join(samples, ", "))

	return fmt.Sprintf(
		"Symbol grounding check: You referenced %d code symbols that haven't been verified through tool calls "+
			"(e.g., %s). Before editing or modifying these, use read_file, grep, or lsp_symbols to confirm "+
			"they exist and match your understanding. Hallucinated symbol references are a leading cause of "+
			"edit failures and wrong-file modifications.",
		len(candidates), strings.Join(samples, ", "))
}

func isCommonPath(path string) bool {
	lower := strings.ToLower(path)
	for _, c := range []string{"node_modules/", "vendor/", ".git/", "dist/", "build/"} {
		if strings.Contains(lower, c) {
			return true
		}
	}
	return false
}
