package agent

// Multi-language delimiter balance validation for non-Go source files.
//
// Research basis: The SWE-agent paper ("Agent-Computer Interfaces Enable Software
// Engineering Language Models") demonstrated that ACI quality — specifically
// immediate post-edit feedback — is one of the single biggest factors in agent
// performance. Linting/formatting after edits, rich error messages, and
// structural validation all significantly improve success rates.
//
// ggcode's write_integrity.go validates Go syntax via go/parser, but for all
// other languages (TypeScript, JavaScript, Python, Rust, Java, Dart, C/C++,
// JSON, YAML, CSS) there is NO structural validation after edits. The agent can
// write code with unbalanced brackets — a very common edit failure — and get
// zero feedback until the build fails, wasting a full build/test cycle.
//
// Competitor analysis:
//   - Claude Code: uses LSP for post-edit feedback (requires running server)
//   - Cursor: in-process diagnostics (IDE-bound, not available in CLI)
//   - Aider: lightweight syntax checks before commit
//   - OpenHands/Cline: post-edit build verification (slow feedback loop)
//
// This module fills the gap with a zero-dependency, always-available delimiter
// balance check that catches the most common structural issue (unbalanced
// brackets) in <1ms per file. It is string/comment-aware to avoid false
// positives, and supports Python triple-quoted strings.

import (
	"fmt"
	"path/filepath"
	"strings"
)

// checkDelimiterBalance validates that brackets (), {}, [] are balanced in
// source files, ignoring delimiters inside strings and comments. Returns a
// non-empty warning string if an imbalance is detected.
//
// Only runs on file types where bracket balance is syntactically significant
// and where no dedicated parser already exists in the integrity pipeline (Go
// files are excluded because go/parser is strictly more powerful).
func checkDelimiterBalance(filePath, content string) string {
	if !shouldCheckDelimiters(filePath) {
		return ""
	}
	if strings.TrimSpace(content) == "" {
		return ""
	}

	msg := scanDelimiters(content, getCommentStyle(filePath))
	if msg == "" {
		return ""
	}

	return "[delimiter imbalance] " + msg + " — this will likely cause a syntax or build error."
}

// shouldCheckDelimiters returns true for file types where bracket balance is
// syntactically significant and not already covered by a language-specific
// parser in the integrity pipeline.
func shouldCheckDelimiters(filePath string) bool {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs",
		".json",
		".java", ".kt", ".kts",
		".c", ".h", ".cpp", ".cc", ".cxx", ".hpp", ".hh",
		".rs",
		".dart",
		".swift",
		".css", ".scss", ".less",
		".yaml", ".yml",
		".py", ".pyw", ".rb":
		return true
	default:
		return false
	}
}

// commentStyle describes how comments and strings work in a given language.
type commentStyle struct {
	lineComments []string // line comment prefixes, e.g. "//", "#"
	blockOpen    string   // block comment open, e.g. "/*"
	blockClose   string   // block comment close, e.g. "*/"
	tripleQuotes bool     // Python-style """ or ''' multi-line strings
}

// getCommentStyle returns the comment and string syntax for a given file type.
func getCommentStyle(filePath string) commentStyle {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".py", ".pyw":
		return commentStyle{
			lineComments: []string{"#"},
			tripleQuotes: true,
		}
	case ".rb":
		return commentStyle{
			lineComments: []string{"#"},
		}
	case ".yaml", ".yml":
		return commentStyle{
			lineComments: []string{"#"},
		}
	case ".css", ".scss", ".less":
		return commentStyle{
			blockOpen:  "/*",
			blockClose: "*/",
		}
	default:
		// C-like languages: JS, TS, Java, C, C++, Rust, Dart, Swift, Kotlin, JSON
		return commentStyle{
			lineComments: []string{"//"},
			blockOpen:    "/*",
			blockClose:   "*/",
		}
	}
}

// scanner state constants for the delimiter balance state machine.
const (
	dsCode         = iota
	dsStringSingle // '...'
	dsStringDouble // "..."
	dsStringBack   // `...`
	dsLineComment  // //... or #...
	dsBlockComment // /* ... */
	dsTripleDouble // """ ... """
	dsTripleSingle // ''' ... '''
)

// openBracket tracks an opening delimiter and its line number for diagnostics.
type openBracket struct {
	char byte
	line int
}

// scanDelimiters scans content for unbalanced (), {}, [] outside of strings and
// comments. Returns an empty string if balanced, or a diagnostic message.
func scanDelimiters(content string, style commentStyle) string {
	var stack []openBracket
	line := 1
	state := dsCode

	i := 0
	n := len(content)

	for i < n {
		c := content[i]

		// Line tracking.
		if c == '\n' {
			line++
			if state == dsLineComment {
				state = dsCode
			}
			i++
			continue
		}

		switch state {
		case dsCode:
			// --- Triple-quoted strings (Python) ---
			if style.tripleQuotes && i+2 < n {
				tri := string(content[i : i+3])
				if tri == `"""` {
					state = dsTripleDouble
					i += 3
					continue
				}
				if tri == `'''` {
					state = dsTripleSingle
					i += 3
					continue
				}
			}

			// --- Block comment open ---
			if style.blockOpen != "" && i+1 < n && content[i:i+len(style.blockOpen)] == style.blockOpen {
				state = dsBlockComment
				i += len(style.blockOpen)
				continue
			}

			// --- Line comment ---
			isLineCmt := false
			for _, lc := range style.lineComments {
				if i+len(lc) <= n && content[i:i+len(lc)] == lc {
					state = dsLineComment
					i += len(lc)
					isLineCmt = true
					break
				}
			}
			if isLineCmt {
				continue
			}

			// --- String starts ---
			if c == '\'' {
				state = dsStringSingle
				i++
				continue
			}
			if c == '"' {
				state = dsStringDouble
				i++
				continue
			}
			if c == '`' {
				state = dsStringBack
				i++
				continue
			}

			// --- Bracket tracking ---
			switch c {
			case '(', '{', '[':
				stack = append(stack, openBracket{char: c, line: line})
			case ')', '}', ']':
				if len(stack) == 0 {
					return fmt.Sprintf("line %d: unexpected '%c' with no matching opening delimiter", line, c)
				}
				top := stack[len(stack)-1]
				if !isMatchingBracket(top.char, c) {
					return fmt.Sprintf("line %d: '%c' does not match '%c' opened at line %d", line, c, top.char, top.line)
				}
				stack = stack[:len(stack)-1]
			}
			i++

		case dsStringSingle:
			if c == '\\' {
				i += 2 // skip escaped char
				continue
			}
			if c == '\'' {
				state = dsCode
			}
			i++

		case dsStringDouble:
			if c == '\\' {
				i += 2
				continue
			}
			if c == '"' {
				state = dsCode
			}
			i++

		case dsStringBack:
			if c == '\\' {
				i += 2
				continue
			}
			if c == '`' {
				state = dsCode
			}
			i++

		case dsLineComment:
			// Consumed by newline handler above.
			i++

		case dsBlockComment:
			if style.blockClose != "" && i+len(style.blockClose) <= n && content[i:i+len(style.blockClose)] == style.blockClose {
				state = dsCode
				i += len(style.blockClose)
				continue
			}
			i++

		case dsTripleDouble:
			if i+2 < n && content[i:i+3] == `"""` {
				state = dsCode
				i += 3
				continue
			}
			if c == '\n' {
				line++
			}
			i++

		case dsTripleSingle:
			if i+2 < n && content[i:i+3] == `'''` {
				state = dsCode
				i += 3
				continue
			}
			if c == '\n' {
				line++
			}
			i++

		default:
			i++
		}
	}

	// Check for unclosed opening delimiters.
	if len(stack) > 0 {
		unclosed := stack[0] // outermost unclosed
		return fmt.Sprintf("line %d: unclosed '%s' — missing closing delimiter", unclosed.line, bracketName(unclosed.char))
	}

	return ""
}

// isMatchingBracket returns true if open and close form a matching pair.
func isMatchingBracket(open, close byte) bool {
	return (open == '(' && close == ')') ||
		(open == '{' && close == '}') ||
		(open == '[' && close == ']')
}

// bracketName returns the human-readable pair name for an opening bracket.
func bracketName(open byte) string {
	switch open {
	case '(':
		return "()"
	case '{':
		return "{}"
	case '[':
		return "[]"
	default:
		return string(open)
	}
}
