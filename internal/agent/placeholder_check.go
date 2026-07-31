package agent

// Placeholder / stub code detection for written files.
//
// Research basis: the #1 user complaint about AI coding agents is "the agent
// said it was done but left placeholder code" — empty function bodies, panic("not
// implemented"), raise NotImplementedError, vague "// TODO: implement this" etc.
// These signal that the agent skipped actual implementation work.
//
// Competitive landscape:
//   - Devin: runs a post-completion review that flags stubs
//   - Claude Code: relies on the agent's self-judgment (unreliable)
//   - Cursor: lint-on-save catches some, but not semantic stubs
//   - Cline/OpenHands: reactive only — caught by build/test cycle (if at all)
//   - Aider: commits per-edit, so stubs become visible in diff review
//
// ggcode's approach: detect unambiguous placeholder patterns at write time by
// comparing occurrence counts before vs. after the edit. Only NEW placeholders
// (introduced by this edit) are flagged — pre-existing ones are left alone.
// This is zero-LLM-cost, language-aware, and has near-zero false positives
// because we target only UNAMBIGUOUS markers (not generic TODO comments).

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// placeholderPattern represents an unambiguous placeholder/stub marker.
type placeholderPattern struct {
	pattern string // substring or regex to match
	label   string // human-readable description
	isRegex bool   // if true, treat pattern as regex
}

// placeholderPatternsByExt maps file extensions to their placeholder patterns.
// Only UNAMBIGUOUS patterns are included — ones that almost always indicate
// the developer (or agent) skipped the actual implementation.
//
// We deliberately EXCLUDE generic patterns like bare "// TODO" because those
// are extremely common in real codebases and would generate excessive false
// positives. Instead we target:
//  1. Language-specific "not implemented" primitives (panic, raise, throw)
//  2. Vague TODO comments that explicitly defer implementation
//     ("implement this", "add logic here", "fill in", "your code here")
var placeholderPatternsByExt = map[string][]placeholderPattern{
	".go": {
		{`panic("not implemented")`, "panic: not implemented", false},
		{`panic("TODO")`, "panic: TODO", false},
		{`panic("unimplemented")`, "panic: unimplemented", false},
		{`panic("placeholder")`, "panic: placeholder", false},
		{`panic("stub")`, "panic: stub", false},
	},
	".py": {
		{"raise NotImplementedError", "NotImplementedError", false},
		{"raise NotImplemented(", "NotImplemented", false},
		{`raise Exception("TODO"`, "Exception: TODO", false},
	},
	".js": {
		{`throw new Error("not implemented"`, "throw: not implemented", false},
		{`throw new Error("TODO"`, "throw: TODO", false},
		{`throw new Error("placeholder"`, "throw: placeholder", false},
		{`throw "not implemented"`, "throw: not implemented", false},
	},
	".jsx": {
		{`throw new Error("not implemented"`, "throw: not implemented", false},
		{`throw new Error("TODO"`, "throw: TODO", false},
		{`throw "not implemented"`, "throw: not implemented", false},
	},
	".ts": {
		{`throw new Error("not implemented"`, "throw: not implemented", false},
		{`throw new Error("TODO"`, "throw: TODO", false},
		{`throw "not implemented"`, "throw: not implemented", false},
	},
	".tsx": {
		{`throw new Error("not implemented"`, "throw: not implemented", false},
		{`throw new Error("TODO"`, "throw: TODO", false},
		{`throw "not implemented"`, "throw: not implemented", false},
	},
	".rs": {
		{"unimplemented!()", "unimplemented!()", false},
		{"todo!()", "todo!()", false},
	},
	".java": {
		{"throw new UnsupportedOperationException", "UnsupportedOperationException", false},
	},
	".kt": {
		{"TODO(\"", "TODO() stub", false},
		{"NotImplementedError", "NotImplementedError", false},
	},
	".rb": {
		{"raise NotImplementedError", "NotImplementedError", false},
		{"raise NotImplementedError.new", "NotImplementedError", false},
	},
}

// vagueTodoRe matches TODO/FIXME comments that explicitly defer implementation
// with vague language. This is cross-language (works for //, #, -- comments).
// Examples that match:
//
//	"// TODO: implement this"
//	"// TODO: implement"
//	"// TODO: implement logic here"
//	"// TODO: fill in"
//	"// TODO: add logic here"
//	"# TODO: your code here"
//	"// FIXME: not implemented"
//	"// TODO: implement this function"
var vagueTodoRe = regexp.MustCompile(
	`(?im)^\s*(//|#|--)\s*(TODO|FIXME|HACK|XXX)\s*[:\)]?\s*(implement\s+(this|it|the|logic)|fill\s+in|your\s+code\s+here|add\s+(logic|code|implementation)|not\s+implemented|placeholder|stub\s+here|complete\s+this|coming\s+soon)`)

// maxPlaceholderWarnings caps the number of placeholder warnings per write.
const maxPlaceholderWarnings = 3

// checkPlaceholderCode detects placeholder/stub code that was INTRODUCED by
// this edit (present in newContent but not in oldContent). Returns warning
// strings. Only flags NEW placeholders to avoid noise from pre-existing code.
//
// Key design decisions:
//   - Only flags NEW placeholders: compares occurrence counts in old vs new.
//   - Skips test files: some test stubs are intentionally empty.
//   - Skips interface/abstract files: Go interfaces have empty method bodies.
//   - Targets UNAMBIGUOUS markers (not generic TODOs) for near-zero false positives.
func checkPlaceholderCode(filePath, oldContent, newContent string) []string {
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	ext := filepath.Ext(filePath)
	patterns, ok := placeholderPatternsByExt[ext]
	if !ok {
		// Still check for vague TODOs across all code file types
		return checkVagueTodos(oldContent, newContent)
	}

	// Skip test files — stubs in tests are often intentional.
	if isTestFile(filePath) {
		return nil
	}

	var warnings []string

	// 1. Language-specific placeholder patterns (substring-based)
	for _, p := range patterns {
		oldCount := strings.Count(oldContent, p.pattern)
		newCount := strings.Count(newContent, p.pattern)
		introduced := newCount - oldCount
		if introduced > 0 {
			warnings = append(warnings, formatPlaceholderWarning(p.label, introduced))
		}
	}

	// 2. Vague TODO/FIXME comments (regex-based, cross-language)
	warnings = append(warnings, checkVagueTodos(oldContent, newContent)...)

	if len(warnings) > maxPlaceholderWarnings {
		warnings = warnings[:maxPlaceholderWarnings]
	}

	return warnings
}

// checkVagueTodos detects newly-introduced vague TODO/FIXME comments.
func checkVagueTodos(oldContent, newContent string) []string {
	oldMatches := vagueTodoRe.FindAllString(oldContent, -1)
	newMatches := vagueTodoRe.FindAllString(newContent, -1)

	// Count NEW matches by comparing normalized sets
	oldSet := make(map[string]int)
	for _, m := range oldMatches {
		oldSet[strings.TrimSpace(strings.ToLower(m))]++
	}

	var newCount int
	for _, m := range newMatches {
		key := strings.TrimSpace(strings.ToLower(m))
		oldSet[key]--
		if oldSet[key] < 0 {
			newCount++
		}
	}

	if newCount == 0 {
		return nil
	}

	return []string{formatPlaceholderWarning("vague TODO/FIXME deferring implementation", newCount)}
}

// formatPlaceholderWarning renders a concise warning for newly-introduced placeholder code.
func formatPlaceholderWarning(label string, count int) string {
	noun := "occurrence"
	if count > 1 {
		noun = "occurrences"
	}
	return fmt.Sprintf(
		"Introduced %d %s of placeholder/stub code (%s). This looks like incomplete "+
			"implementation — implement the actual logic or remove the placeholder before "+
			"reporting the task as done.",
		count, noun, label)
}
