package knight

import (
	"regexp"
	"strings"
)

// genericCandidateNames are too vague to carry meaning as a standalone skill
// name. Knight rejects candidates that collapse to one of these.
var genericCandidateNames = map[string]struct{}{
	"tool":             {},
	"tools":            {},
	"llm":              {},
	"lsp":              {},
	"build":            {},
	"test":             {},
	"fix":              {},
	"check":            {},
	"run":              {},
	"code":             {},
	"skill":            {},
	"skills":           {},
	"context":          {},
	"agent":            {},
	"prompt":           {},
	"command":          {},
	"commands":         {},
	"util":             {},
	"helper":           {},
	"workflow":         {},
	"convention":       {},
	"conventions":      {},
	"build-convention": {},
}

var nameAllowedRune = regexp.MustCompile(`[^a-z0-9-]+`)
var nameMultiDash = regexp.MustCompile(`-+`)

// sanitizeCandidateName normalizes a raw candidate name to lower-kebab-case,
// strips disallowed runes, collapses runs of dashes, and trims leading/trailing
// dashes. Empty result indicates the name is unusable.
func sanitizeCandidateName(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return ""
	}
	s = nameAllowedRune.ReplaceAllString(s, "-")
	s = nameMultiDash.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len([]rune(s)) > 60 {
		s = strings.Trim(string([]rune(s)[:60]), "-")
	}
	return s
}

// candidateNameAcceptable reports whether a sanitized name is meaningful enough
// to keep. It rejects empty names, single-token generics, and names dominated
// by short tokens (e.g. "lsp-tool" -> two short generics joined).
func candidateNameAcceptable(name string) bool {
	if name == "" {
		return false
	}
	if len(name) < 4 {
		return false
	}
	if _, generic := genericCandidateNames[name]; generic {
		return false
	}
	parts := strings.Split(name, "-")
	if len(parts) < 2 {
		return false
	}
	informative := 0
	allGeneric := true
	for _, p := range parts {
		if _, gen := genericCandidateNames[p]; !gen {
			allGeneric = false
		}
		if len(p) >= 4 {
			informative++
		}
	}
	if allGeneric {
		return false
	}
	if informative >= 1 {
		return true
	}
	// Allow short alphanumeric mixes like "llm-200" if the overall length is
	// substantial enough to carry meaning.
	return len(name) >= 6
}
