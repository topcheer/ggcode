package agent

// Self-Declared Constraint Violation Detector
//
// Research basis:
//   - AgentRx (arXiv:2602.02475, Feb 2026): step-level constraint violation
//     tracking -- agents synthesize constraints from their task, then silently
//     violate those self-declared constraints in later tool calls. The paper
//     shows that cross-domain agent failures frequently stem from the agent
//     declaring a scoping or avoidance constraint in its reasoning, then
//     acting against it.
//   - Trajectory-Informed Memory Generation (arXiv:2603.10600, 2026): agents
//     repeat inefficient patterns and fail to recover because they don't
//     track their own declared boundaries across a run.
//   - "Why AI Coding Agents Fail" (beginnersinai.org, 2026): scope violation
//     -- editing files the agent said it wouldn't touch -- is a top-5 failure
//     mode in coding agents.
//
// Problem: AI coding agents frequently declare constraints in their own
// reasoning text:
//
//   "I'll only modify files in the auth/ directory"
//   "I should not touch the public API"
//   "I must avoid changing the database schema"
//   "Let me limit my changes to the handler layer"
//   "I won't modify any test files"
//
// But after several tool calls, the agent edits a file that violates its own
// declared constraint. This is invisible to the user and silently expands
// scope beyond what the agent committed to.
//
// Existing ggcode detectors that are RELATED but do NOT cover this:
//   - constraint_amnesia.go: tracks USER-specified constraints and reminds
//     the agent when context grows large (forgetting prevention). Does NOT
//     check violations. Does NOT extract agent-self-declared constraints.
//   - scope_drift.go: tracks file diversity / scope expansion generically.
//     Does NOT match against specific declared constraints.
//   - premature_refactor.go: detects unnecessary refactoring before feature
//     completion. Does NOT check declared boundaries.
//   - tool_target_mismatch.go: compares stated intent vs actual tool target
//     for single actions. Does NOT track constraints across multiple steps.
//
// Gap: No detector extracts constraints from the AGENT'S OWN reasoning text
// and then validates subsequent tool calls against those self-declared
// constraints. This detector fills that gap.
//
// Design:
//   - Extracts self-declared constraints from assistant reasoning text via
//     pattern matching (scope declarations, avoidance declarations)
//   - Tracks each constraint with its source iteration
//   - On each tool call with file-path arguments, checks whether the path
//     violates any tracked constraint
//   - Two violation types:
//     (a) Scope-exceeding: tool targets a path OUTSIDE a declared scope
//     (b) Direct violation: tool targets a path matching an avoidance constraint
//   - Zero LLM cost -- pure deterministic pattern matching
//   - Fires at most 2 warnings per run (advisory, non-blocking)

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

// cvLeaveAloneRe matches "leave X alone" / "leaving X alone" phrasing where
// X is the path/module the agent promises not to touch. The captured group
// extracts the constraint target.
var cvLeaveAloneRe = regexp.MustCompile(`(?i)leav\w+ (.+?) alone`)

// parseToolArgs unmarshals raw JSON tool arguments into a map.
func parseToolArgs(raw json.RawMessage) map[string]any {
	var args map[string]any
	_ = json.Unmarshal(raw, &args)
	if args == nil {
		args = map[string]any{}
	}
	return args
}

const (
	// cvMaxTracked: max self-declared constraints to track.
	cvMaxTracked = 10

	// cvMaxWarnings: max violation warnings per run to avoid noise.
	cvMaxWarnings = 2

	// cvExcerptLen: max chars per constraint excerpt.
	cvExcerptLen = 100
)

// cvConstraint represents a self-declared constraint extracted from agent
// reasoning text.
type cvConstraint struct {
	excerpt     string // the constraint text
	iter        int    // iteration when declared
	constraintT string // "scope" or "avoid"
	pattern     string // the extracted path/directory/module pattern
}

// constraintViolationState tracks agent self-declared constraints and detects
// violations in subsequent tool calls.
type constraintViolationState struct {
	constraints []cvConstraint
	warnings    int
	currentIter int
}

func newConstraintViolationState() *constraintViolationState {
	return &constraintViolationState{}
}

func (s *constraintViolationState) reset() {
	s.constraints = nil
	s.warnings = 0
	s.currentIter = 0
}

// recordReasoning extracts self-declared constraints from assistant reasoning
// text. Called after the assistant response is captured.
func (s *constraintViolationState) recordReasoning(text string, iter int) {
	if len(s.constraints) >= cvMaxTracked {
		return
	}
	s.currentIter = iter
	extracted := cvExtractConstraints(text, iter)
	for _, c := range extracted {
		if len(s.constraints) >= cvMaxTracked {
			break
		}
		// Deduplicate: skip if we already track a constraint with the same
		// pattern and type.
		dup := false
		for _, existing := range s.constraints {
			if existing.constraintT == c.constraintT && existing.pattern == c.pattern {
				dup = true
				break
			}
		}
		if !dup {
			s.constraints = append(s.constraints, c)
		}
	}
}

// checkToolCall validates a tool call against tracked constraints.
// Returns a non-empty warning message if a violation is detected.
func (s *constraintViolationState) checkToolCall(toolName string, args map[string]any, iter int) string {
	if s.warnings >= cvMaxWarnings || len(s.constraints) == 0 {
		return ""
	}

	// Only check tools that modify files.
	path := cvExtractPath(args)
	if path == "" {
		return ""
	}

	path = filepath.ToSlash(path)

	for _, c := range s.constraints {
		violated, detail := cvCheckViolation(c, path)
		if violated {
			s.warnings++
			// #1029: c.excerpt comes from cvExtractExcerpt which already caps
			// at cvExcerptLen runes, so this dead re-truncation (span is always
			// <= 70 bytes, never > 100) is removed.
			msg := fmt.Sprintf(
				"[Self-Declared Constraint Violation] In iteration %d you declared: %q\n"+
					"but your current tool call (%s) targets '%s', which %s.\n"+
					"This violates your own stated boundary. Reconsider: either narrow the "+
					"tool target to comply with your constraint, or explicitly revise the "+
					"constraint with justification if the scope legitimately needs expansion.\n"+
					"(Research basis: AgentRx arXiv:2602.02475 -- step-level constraint violation tracking)",
				c.iter, c.excerpt, toolName, path, detail)
			return msg
		}
	}
	return ""
}

// cvExtractPath extracts the primary file-path argument from a tool call.
func cvExtractPath(args map[string]any) string {
	// Most file-editing tools use "file_path" or "path".
	for _, key := range []string{"file_path", "path", "source"} {
		if v, ok := args[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	// multi_edit_file / edit_file also use file_path
	return ""
}

// cvCheckViolation checks if a path violates a constraint.
// Returns (true, human-readable detail) if violated.
func cvCheckViolation(c cvConstraint, path string) (bool, string) {
	pat := strings.ToLower(c.pattern)
	p := strings.ToLower(path)

	switch c.constraintT {
	case "avoid":
		// Avoidance constraint: path must NOT match the pattern.
		if cvPathMatchesPattern(p, pat) {
			return true, fmt.Sprintf("falls within the '%s' you said you would avoid", c.pattern)
		}

	case "scope":
		// Scope constraint: path must be WITHIN the declared scope.
		// If the path does NOT start with the scope pattern, it's outside scope.
		if !cvPathMatchesPattern(p, pat) {
			return true, fmt.Sprintf("is outside the '%s' scope you declared", c.pattern)
		}
	}

	return false, ""
}

// cvPathMatchesPattern checks if a path matches/contains a pattern.
// Uses prefix and substring matching since constraint patterns are typically
// directory names or module names extracted from natural language.
func cvPathMatchesPattern(path, pattern string) bool {
	if pattern == "" {
		return false
	}
	// Normalize: strip leading/trailing slashes for flexible matching.
	p := strings.TrimPrefix(path, "/")
	pat := strings.TrimPrefix(pattern, "/")
	pat = strings.TrimSuffix(pat, "/")

	// Direct prefix match.
	if strings.HasPrefix(p, pat) {
		return true
	}
	// Path component match: pattern is a directory/module name.
	// Matches as a complete path segment (e.g., "auth" matches
	// "internal/auth/handler.go" but NOT "internal/authorization/handler.go").
	// Also matches compound path segments at word boundaries using
	// delimiters _, -, . (e.g., "test" matches "foo_test" or "test_bar"
	// but not "testing" or "latest").
	parts := strings.Split(p, "/")
	for _, part := range parts {
		if part == pat {
			return true
		}
		if len(pat) >= 3 {
			tokens := strings.FieldsFunc(part, func(r rune) bool {
				return r == '_' || r == '-' || r == '.'
			})
			for _, tok := range tokens {
				if tok == pat {
					return true
				}
			}
		}
	}
	// Also check if pattern is a directory prefix.
	if strings.HasPrefix(p, pat+"/") || strings.HasPrefix(pat, p+"/") {
		return true
	}
	return false
}

// cvExtractConstraints extracts self-declared constraints from reasoning text.
// Returns a slice of cvConstraint values.
func cvExtractConstraints(text string, iter int) []cvConstraint {
	var result []cvConstraint
	lower := strings.ToLower(text)

	// --- Scope constraints ---
	// Patterns: "I'll only modify X", "limiting changes to X",
	// "staying within X", "restricting to X", "only touching X"
	scopePatterns := []string{
		"only modify",
		"only touch",
		"only change",
		"only edit",
		"limiting changes to",
		"limit my changes to",
		"limit changes to",
		"staying within",
		"stay within",
		"restricting to",
		"restrict changes to",
		"scoped to",
		"changes are limited to",
	}
	for _, sp := range scopePatterns {
		if idx := strings.Index(lower, sp); idx >= 0 {
			pattern := cvExtractPathAfter(lower, idx+len(sp))
			if pattern != "" {
				result = append(result, cvConstraint{
					excerpt:     cvExtractExcerpt(text, idx, 60),
					iter:        iter,
					constraintT: "scope",
					pattern:     pattern,
				})
			}
		}
	}

	// --- Avoidance constraints ---
	// Patterns: "should not touch X", "must avoid X", "won't modify X",
	// "not changing X", "leave X alone", "don't edit X"

	// "leave/leaving X alone" has a different structure (path between two
	// keywords), so handle it with a regex before the literal-pattern loop.
	if m := cvLeaveAloneRe.FindStringSubmatch(lower); len(m) > 1 {
		path := strings.TrimSpace(m[1])
		// Strip common articles/connectors so the pattern matches file paths.
		for _, prefix := range []string{"the ", "any ", "all ", "files in ", "files "} {
			path = strings.TrimPrefix(path, prefix)
		}
		path = strings.TrimSpace(path)
		if path != "" && len(path) <= 80 {
			idx := strings.Index(lower, m[0])
			if idx >= 0 {
				result = append(result, cvConstraint{
					excerpt:     cvExtractExcerpt(text, idx, 60),
					iter:        iter,
					constraintT: "avoid",
					pattern:     path,
				})
			}
		}
	}

	avoidPatterns := []string{
		"should not touch",
		"shouldn't touch",
		"should not modify",
		"shouldn't modify",
		"should not change",
		"should not edit",
		"must avoid",
		"must not touch",
		"must not modify",
		"must not change",
		"won't modify",
		"won't touch",
		"won't change",
		"won't edit",
		"will not modify",
		"will not touch",
		"will not change",
		"will not edit",
		"not changing",
		"not modifying",
		"not editing",
		"don't edit",
		"don't modify",
		"don't change",
		"do not edit",
		"do not modify",
		"do not change",
		"avoid touching",
		"avoid modifying",
		"avoid changing",
	}
	for _, ap := range avoidPatterns {
		if idx := strings.Index(lower, ap); idx >= 0 {
			pattern := cvExtractPathAfter(lower, idx+len(ap))
			if pattern != "" {
				result = append(result, cvConstraint{
					excerpt:     cvExtractExcerpt(text, idx, 60),
					iter:        iter,
					constraintT: "avoid",
					pattern:     pattern,
				})
			}
		}
	}

	return result
}

// cvExtractPathAfter tries to extract a file path or directory/module name
// from text starting at the given offset. Looks for quoted paths, backtick
// paths, or path-like tokens.
func cvExtractPathAfter(lowerText string, offset int) string {
	if offset >= len(lowerText) {
		return ""
	}
	rest := lowerText[offset:]

	// Skip leading whitespace and connectors ("the ", "any ", "all ").
	rest = strings.TrimLeft(rest, " \t")
	changed := true
	for changed {
		changed = false
		for _, prefix := range []string{"the ", "any ", "all ", "files in ", "files ", "in "} {
			if newRest := strings.TrimPrefix(rest, prefix); newRest != rest {
				rest = newRest
				changed = true
				break
			}
		}
	}

	if rest == "" {
		return ""
	}

	// Case 1: Quoted path (single or double quotes, backticks).
	if len(rest) > 0 && (rest[0] == '\'' || rest[0] == '"' || rest[0] == '`') {
		quote := rest[0]
		end := strings.IndexByte(rest[1:], quote)
		if end > 0 {
			return strings.TrimSpace(rest[1 : 1+end])
		}
	}

	// Case 2: Path-like token (contains / and looks like a file path).
	// Read until we hit punctuation or whitespace.
	end := len(rest)
	for i, ch := range rest {
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == ',' ||
			ch == ';' || ch == '.' || ch == '!' || ch == ')' {
			// Allow periods inside paths like "internal/agent/agent.go"
			// -- only cut on period if it's at end of word (followed by space/end).
			if ch == '.' && i+1 < len(rest) && rest[i+1] != ' ' && rest[i+1] != '\n' {
				continue
			}
			end = i
			break
		}
	}
	token := strings.TrimSpace(rest[:end])
	token = strings.TrimRight(token, ".,;!")

	// Only accept tokens that look like paths (contain /) or known module keywords.
	if strings.Contains(token, "/") {
		return token
	}

	// Accept common module/directory keywords even without slashes.
	moduleKeywords := map[string]bool{
		"auth": true, "config": true, "agent": true, "handler": true,
		"controller": true, "model": true, "service": true, "util": true,
		"utils": true, "test": true, "tests": true, "api": true,
		"database": true, "db": true, "schema": true, "migration": true,
		"frontend": true, "backend": true, "client": true, "server": true,
		"cmd": true, "internal": true, "pkg": true, "vendor": true,
		"middleware": true, "router": true, "view": true, "views": true,
	}
	if moduleKeywords[token] {
		return token
	}

	// Check for compound: "the X directory/module/layer/package"
	for _, suffix := range []string{" directory", " module", " layer", " package", " folder"} {
		if strings.HasSuffix(rest[:end], suffix) {
			name := strings.TrimSpace(rest[:end-len(suffix)])
			name = strings.TrimPrefix(name, "the ")
			if name != "" {
				return name
			}
		}
	}

	return ""
}

// cvExtractExcerpt extracts a human-readable excerpt from the original text
// around the given position.
func cvExtractExcerpt(text string, pos, length int) string {
	// #1029: back the start up to a rune boundary -- the unconditional
	// pos-10 byte slice can split a multi-byte rune and leak invalid UTF-8
	// into the warning text.
	start := pos - 10
	if start < 0 {
		start = 0
	} else {
		for start > 0 && !utf8.RuneStart(text[start]) {
			start--
		}
	}
	end := pos + length
	if end > len(text) {
		end = len(text)
	}
	excerpt := strings.TrimSpace(text[start:end])
	// Clean up newlines for display.
	excerpt = strings.ReplaceAll(excerpt, "\n", " ")
	if len(excerpt) > cvExcerptLen {
		// #1029: rune-safe truncation.
		runes := []rune(excerpt)
		cut := cvExcerptLen
		if cut > len(runes) {
			cut = len(runes)
		}
		excerpt = string(runes[:cut]) + "..."
	}
	return excerpt
}
