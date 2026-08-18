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

	// fix #730 (same family as #723/#728): strip comments and docstring
	// bodies from both versions BEFORE the multiset comparison, so MENTIONS
	// of placeholder patterns inside comments or docstrings (e.g. "// legacy
	// path used to panic(...)" or a Python docstring documenting a raise) do
	// not count as introduced placeholders. checkVagueTodos intentionally
	// still runs on the RAW content — its patterns are TODO comments by
	// definition and must keep matching comments.
	oldStripped := stripPlaceholderComments(oldContent, ext)
	newStripped := stripPlaceholderComments(newContent, ext)

	// 1. Language-specific placeholder patterns (substring-based).
	// Position-aware comparison (fix #171/#175): fixed substrings like
	// `panic("TODO")` are identical everywhere, so we use trimmed line content
	// to distinguish actual new occurrences from moved ones (fix #572 Bug C).
	for _, p := range patterns {
		oldLines := substringLineMultiset(oldStripped, p.pattern)
		newLines := substringLineMultiset(newStripped, p.pattern)
		introduced := 0
		for line, cnt := range newLines {
			if old := oldLines[line]; cnt > old {
				introduced += cnt - old
			}
		}
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

// stripPlaceholderComments removes comments (and Python docstring bodies)
// from content so MENTIONS of placeholder patterns inside comments or
// docstrings do not count toward the multiset comparison (fix #730, same
// family as #723 for insecure-pattern comment FPs and #728 for block-comment
// body lines).
//
// Reuses the shared #723/#728 helpers for C-style languages
// (cStyleBlockCommentLine + goStripTrailingComment). For Python/Ruby a
// dedicated line stripper (pyStripCommentsKeepStrings) removes `#` comments
// and triple-quoted docstring bodies — with cross-line docstring state — but
// deliberately KEEPS single-quoted string literals: several real placeholder
// patterns contain string literals (e.g. `raise Exception("TODO")`), which
// the shared pyStripCommentsAndStrings would drop (its string-dropping
// semantics are correct for insecure-pattern detection but would create
// false negatives here).
//
// Stripped-to-empty lines are harmless: substringLineMultiset keys on
// trimmed line content, so an empty line never contains any pattern.
func stripPlaceholderComments(content, ext string) string {
	switch ext {
	case ".py", ".rb":
		return pyStripCommentsKeepStrings(content)
	case ".go", ".js", ".jsx", ".ts", ".tsx", ".rs", ".java", ".kt":
		return cStyleStripComments(content)
	default:
		return content
	}
}

// cStyleStripComments strips // and /* */ comments from C-style source
// content (Go, JS/TS, Rust, Java, Kotlin), reusing the #723/#728 shared
// helpers: cStyleBlockCommentLine for block-comment state tracking and
// full-line comments, goStripTrailingComment for trailing comments on code
// lines. String literals are untouched.
func cStyleStripComments(content string) string {
	lines := strings.Split(content, "\n")
	inBlock := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		code, ok := cStyleBlockCommentLine(trimmed, &inBlock)
		if !ok {
			lines[i] = ""
			continue
		}
		lines[i] = goStripTrailingComment(code)
	}
	return strings.Join(lines, "\n")
}

// pyStripCommentsKeepStrings removes `#` comments and triple-quoted
// (doc)string bodies from Python/Ruby source content, tracking docstring
// state ACROSS lines (unlike the per-line pyStripCommentsAndStrings, a
// multi-line docstring's body lines are fully dropped too). Single-quoted
// string literals are kept verbatim so quote-containing placeholder
// patterns (raise Exception("TODO")) remain detectable (fix #730).
func pyStripCommentsKeepStrings(content string) string {
	lines := strings.Split(content, "\n")
	inDoc := false
	var docQ rune
	for i, line := range lines {
		if inDoc {
			closer := pyFindTripleQuote(line, docQ)
			if closer < 0 {
				lines[i] = ""
				continue
			}
			// Docstring closes on this line; process the remainder as code.
			line = line[closer+3:]
			inDoc = false
		}
		lines[i] = pyStripLineKeepStrings(line, &inDoc, &docQ)
	}
	return strings.Join(lines, "\n")
}

// pyStripLineKeepStrings strips `#` comments and docstring openers from one
// line, copying single-quoted string literals through unchanged.
func pyStripLineKeepStrings(line string, inDoc *bool, docQ *rune) string {
	var b strings.Builder
	r := []rune(line)
	n := len(r)
	i := 0
	for i < n {
		c := r[i]
		if c == '#' {
			break // comment: rest of line ignored
		}
		if c == '"' || c == '\'' {
			var lit string
			lit, i = pyScanQuoted(r, i, inDoc, docQ)
			b.WriteString(lit)
			continue
		}
		b.WriteRune(c)
		i++
	}
	return b.String()
}

// pyScanQuoted consumes a quoted span starting at rune index i (r[i] is the
// opening quote). Triple-quoted (doc)strings are dropped: contents are
// skipped if the span closes on this line, otherwise the cross-line docstring
// state is opened and the rest of the line is dropped. Single-quoted strings
// are returned verbatim (delimiters included). Returns the text to emit (may
// be empty) and the next rune index.
func pyScanQuoted(r []rune, i int, inDoc *bool, docQ *rune) (string, int) {
	n := len(r)
	c := r[i]
	if i+2 < n && r[i+1] == c && r[i+2] == c {
		// Triple-quoted (doc)string.
		j := i + 3
		for j+2 < n {
			if r[j] == c && r[j+1] == c && r[j+2] == c {
				return "", j + 3 // closed on this line: drop contents
			}
			j++
		}
		*inDoc = true
		*docQ = c
		return "", n // unterminated: drop rest of line
	}
	// Single-quoted string: keep the literal verbatim.
	j := i + 1
	for j < n && r[j] != c {
		j++
	}
	if j < n {
		return string(r[i : j+1]), j + 1
	}
	return string(r[i:]), n // unterminated: keep rest of line
}

// pyFindTripleQuote returns the rune index of a triple quote `qqq` in line,
// or -1 if absent.
func pyFindTripleQuote(line string, q rune) int {
	r := []rune(line)
	for j := 0; j+2 < len(r); j++ {
		if r[j] == q && r[j+1] == q && r[j+2] == q {
			return j
		}
	}
	return -1
}

// substringLineMultiset returns a map of trimmed line content → occurrence count of
// substr in content (fix #572 Bug C: use content, not line numbers, to avoid FP when
// lines are inserted above existing placeholders).
func substringLineMultiset(content, substr string) map[string]int {
	lines := make(map[string]int)
	if substr == "" || content == "" {
		return lines
	}
	contentLines := strings.Split(content, "\n")
	for _, line := range contentLines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, substr) {
			lines[trimmed]++
		}
	}
	return lines
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
