package agent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

var cvLeaveAloneRe = regexp.MustCompile(`(?i)leav\w+ (.+?) alone`)

func parseToolArgs(raw json.RawMessage) map[string]any {
	var args map[string]any
	_ = json.Unmarshal(raw, &args)
	if args == nil {
		args = map[string]any{}
	}
	return args
}

const (
	cvMaxTracked = 10

	cvMaxWarnings = 2

	cvExcerptLen = 100
)

type cvConstraint struct {
	excerpt     string
	iter        int
	constraintT string
	pattern     string
}

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

func (s *constraintViolationState) recordReasoning(text string, iter int) {
	if len(s.constraints) >= cvMaxTracked {
		return
	}
	s.currentIter = iter
	extracted := cvExtractConstraints(text, iter)

	var newScopes []cvConstraint
	for _, c := range extracted {
		if c.constraintT == "scope" {
			newScopes = append(newScopes, c)
		}
	}
	if len(newScopes) > 0 {
		kept := s.constraints[:0]
		for _, existing := range s.constraints {
			if existing.constraintT == "scope" {
				continue
			}
			kept = append(kept, existing)
		}
		s.constraints = kept
	}

	for _, c := range extracted {
		if len(s.constraints) >= cvMaxTracked {
			break
		}

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

func (s *constraintViolationState) checkToolCall(toolName string, args map[string]any, iter int) string {
	if s.warnings >= cvMaxWarnings || len(s.constraints) == 0 {
		return ""
	}

	if !sourceMutatingTools[toolName] {
		return ""
	}
	path := cvExtractPath(args)
	if path == "" {
		return ""
	}

	path = filepath.ToSlash(path)

	for _, c := range s.constraints {
		violated, detail := cvCheckViolation(c, path)
		if violated {
			s.warnings++

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

func cvExtractPath(args map[string]any) string {

	for _, key := range []string{"file_path", "path", "source"} {
		if v, ok := args[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}

	return ""
}

func cvCheckViolation(c cvConstraint, path string) (bool, string) {
	pat := strings.ToLower(c.pattern)
	p := strings.ToLower(path)

	switch c.constraintT {
	case "avoid":

		if cvPathMatchesPattern(p, pat) {
			return true, fmt.Sprintf("falls within the '%s' you said you would avoid", c.pattern)
		}

	case "scope":

		if !cvPathMatchesPattern(p, pat) {
			return true, fmt.Sprintf("is outside the '%s' scope you declared", c.pattern)
		}
	}

	return false, ""
}

func cvPathMatchesPattern(path, pattern string) bool {
	if pattern == "" {
		return false
	}

	p := strings.TrimPrefix(path, "/")
	p = strings.TrimPrefix(p, "./")
	pat := strings.TrimPrefix(pattern, "/")
	pat = strings.TrimPrefix(pat, "./")
	pat = strings.TrimSuffix(pat, "/")

	if len(p) >= len(pat) && strings.HasPrefix(p, pat) &&
		(len(p) == len(pat) || p[len(pat)] == '/') {
		return true
	}

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

	if strings.HasPrefix(p, pat+"/") || strings.HasPrefix(pat, p+"/") {
		return true
	}
	return false
}

func cvExtractConstraints(text string, iter int) []cvConstraint {
	var result []cvConstraint
	lower := strings.ToLower(text)

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

	if m := cvLeaveAloneRe.FindStringSubmatch(lower); len(m) > 1 {
		path := strings.TrimSpace(m[1])

		for {
			trimmed := path
			for _, prefix := range []string{"the ", "any ", "all ", "files in ", "files "} {
				trimmed = strings.TrimPrefix(trimmed, prefix)
			}
			for _, suffix := range []string{" files", " file"} {
				trimmed = strings.TrimSuffix(trimmed, suffix)
			}
			trimmed = strings.TrimSpace(trimmed)
			if trimmed == path {
				break
			}
			path = trimmed
		}
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

func cvExtractPathAfter(lowerText string, offset int) string {
	if offset >= len(lowerText) {
		return ""
	}
	rest := lowerText[offset:]

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

	if len(rest) > 0 && (rest[0] == '\'' || rest[0] == '"' || rest[0] == '`') {
		quote := rest[0]
		end := strings.IndexByte(rest[1:], quote)
		if end > 0 {
			return strings.TrimSpace(rest[1 : 1+end])
		}
	}

	for _, suffix := range []string{" files", " file"} {
		rest = strings.TrimSuffix(rest, suffix)
	}

	// Trim directory-suffix words from the full remaining text before the
	// token-terminating scan below: that scan cuts at the first whitespace, so
	// phrases like "the billing module" would otherwise lose the noun phrase
	// ("billing") that precedes the suffix word and yield no pattern (#5006).
	// Trailing sentence punctuation is trimmed first so "... module." still
	// matches, mirroring the TrimRight applied to the plain-token path.
	trimmedRest := strings.TrimRight(rest, " \t\n.,;!)")
	for _, suffix := range []string{" directory", " module", " layer", " package", " folder"} {
		trimmed := strings.TrimSuffix(trimmedRest, suffix)
		if trimmed == trimmedRest {
			continue
		}
		name := strings.TrimSpace(trimmed)
		for _, prefix := range []string{"the ", "any ", "all "} {
			name = strings.TrimPrefix(name, prefix)
		}
		// Keep only the final word of the phrase as the pattern; earlier words
		// are determiners/adjectives like "the new billing".
		if idx := strings.LastIndexAny(name, " \t"); idx >= 0 {
			name = name[idx+1:]
		}
		if name != "" {
			return name
		}
		// Degenerate phrase (bare suffix word): fall through with the suffix
		// removed and let the plain-token path decide.
		rest = trimmed
		break
	}

	end := len(rest)
	for i, ch := range rest {
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == ',' ||
			ch == ';' || ch == '.' || ch == '!' || ch == ')' {

			if ch == '.' && i+1 < len(rest) && rest[i+1] != ' ' && rest[i+1] != '\n' {
				continue
			}
			end = i
			break
		}
	}
	token := strings.TrimSpace(rest[:end])
	token = strings.TrimRight(token, ".,;!")

	if strings.Contains(token, "/") {
		return token
	}

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

	return ""
}

func cvExtractExcerpt(text string, pos, length int) string {

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

	excerpt = strings.ReplaceAll(excerpt, "\n", " ")
	if len(excerpt) > cvExcerptLen {

		runes := []rune(excerpt)
		cut := cvExcerptLen
		if cut > len(runes) {
			cut = len(runes)
		}
		excerpt = string(runes[:cut]) + "..."
	}
	return excerpt
}
