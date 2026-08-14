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

	"github.com/topcheer/ggcode/internal/debug"
)

// checkDelimiterBalance validates that brackets (), {}, [] are balanced in
// source files, ignoring delimiters inside strings and comments. Returns a
// non-empty warning string if an imbalance is detected.
//
// Only runs on file types where bracket balance is syntactically significant
// and where no dedicated parser already exists in the integrity pipeline (Go
// files are excluded because go/parser is strictly more powerful).
// maxDelimiterScanSize is the file size limit for delimiter balance checking.
// Files larger than this are skipped to avoid O(n) scan overhead on very large
// generated/minified files where bracket balance is unlikely to be actionable.
const maxDelimiterScanSize = 1 << 20 // 1MB

func checkDelimiterBalance(filePath, content string) string {
	if !shouldCheckDelimiters(filePath) {
		return ""
	}
	if strings.TrimSpace(content) == "" {
		return ""
	}
	// Skip very large files to avoid unnecessary O(n) scanning overhead.
	if len(content) > maxDelimiterScanSize {
		debug.Log("delimiter", "skipping delimiter check for %s: size %d > %d limit", filePath, len(content), maxDelimiterScanSize)
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
	rust         bool     // Rust-specific handling: lifetimes and r#"..."# raw strings
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
	case ".rs":
		return commentStyle{
			lineComments: []string{"//"},
			blockOpen:    "/*",
			blockClose:   "*/",
			rust:         true,
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

			// --- Rust raw strings r"...", r#"..."#, r##"..."## ... (fix #237, #276) ---
			// Rust allows an arbitrary number of hashes; the string closes only
			// at a '"' followed by the same number of hashes. Content may itself
			// contain "#, which is why counting k is required.
			if style.rust && c == 'r' && i+2 < n && content[i+1] == '"' {
				if end := strings.IndexByte(content[i+2:], '"'); end >= 0 {
					line += strings.Count(content[i:i+2+end+1], "\n")
					i += 2 + end + 1
					continue
				}
				return fmt.Sprintf("line %d: unterminated raw string literal - missing closing \"", line)
			}
			if style.rust && c == 'r' && i+1 < n && content[i+1] == '#' {
				// Count consecutive hashes after 'r'.
				k := 1
				for i+1+k < n && content[i+1+k] == '#' {
					k++
				}
				if i+1+k < n && content[i+1+k] == '"' {
					start := i + 1 + k // index of opening '"'
					if end, ok := indexRawStringClose(content[start+1:], k); ok {
						closed := start + 1 + end + 1 + k // past '"' and k hashes
						line += strings.Count(content[i:closed], "\n")
						i = closed
						continue
					}
					return fmt.Sprintf("line %d: unterminated raw string literal - missing closing quote followed by %d '#'", line, k)
				}
				// 'r' followed by hashes but no quote: not a raw string (e.g.
				// an identifier); fall through to normal handling.
			}

			// --- String starts ---
			if c == '\'' {
				// Rust lifetime vs char literal (fix #237): lifetimes ('a,
				// 'static) never close and must not enter the string state;
				// char literals ('x', '\n') close compactly and contain no
				// brackets, so consuming them as code is safe.
				if style.rust {
					if next, handled := rustSingleQuoteSpan(content, i); handled {
						i = next
						continue
					}
				}
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

	// Check for unclosed string literals, block comments, or triple-quoted
	// strings. An agent edit that leaves a string or comment unterminated
	// causes a syntax error in every language — this is a very common failure
	// mode for partial edits. The scanner state machine tracks these states
	// while scanning (to skip brackets inside them), but previously did not
	// report them if the file ended while still inside one.
	if state != dsCode {
		switch state {
		case dsStringSingle:
			return fmt.Sprintf("line %d: unterminated single-quoted string - missing closing '", unclosedStringLine(content, i))
		case dsStringDouble:
			return fmt.Sprintf("line %d: unterminated double-quoted string - missing closing double-quote", unclosedStringLine(content, i))
		case dsStringBack:
			return fmt.Sprintf("line %d: unterminated template literal - missing closing backtick", unclosedStringLine(content, i))
		case dsBlockComment:
			return fmt.Sprintf("line %d: unterminated block comment - missing closing */", unclosedStringLine(content, i))
		case dsTripleDouble:
			return fmt.Sprintf("line %d: unterminated triple-quoted string - missing closing triple double-quote", unclosedStringLine(content, i))
		case dsTripleSingle:
			return fmt.Sprintf("line %d: unterminated triple-quoted string - missing closing '''", unclosedStringLine(content, i))
		}
	}

	// Check for unclosed opening delimiters.
	if len(stack) > 0 {
		unclosed := stack[0] // outermost unclosed
		return fmt.Sprintf("line %d: unclosed '%s' — missing closing delimiter", unclosed.line, bracketName(unclosed.char))
	}

	return ""
}

// indexRawStringClose returns the index (relative to s) of the '"' that begins
// the closing delimiter of a k-hash Rust raw string — i.e. the first '"'
// followed by exactly k '#' characters (fix #276). ok=false if not found,
// meaning the raw string literal is unterminated.
func indexRawStringClose(s string, k int) (int, bool) {
	for i := 0; i+1+k <= len(s); i++ {
		if s[i] != '"' {
			continue
		}
		closed := true
		for j := 1; j <= k; j++ {
			if s[i+j] != '#' {
				closed = false
				break
			}
		}
		if closed {
			return i, true
		}
	}
	return 0, false
}

// unclosedStringLine returns the line number where the unclosed construct
// started. Since the scanner already advanced past the opening delimiter, we
// approximate by using the current line count (which is where the scanner
// stopped). For multi-line strings/comments, the opening was earlier, but
// reporting the current line still points the agent to the right area.
func unclosedStringLine(content string, pos int) int {
	if pos > len(content) {
		pos = len(content)
	}
	line := 1
	for i := 0; i < pos; i++ {
		if content[i] == '\n' {
			line++
		}
	}
	return line
}

// rustSingleQuoteSpan disambiguates Rust constructs beginning with an
// apostrophe (fix #237):
//
//   - char literals like 'x' or '\n' close compactly right after one
//     (possibly escaped) character; they contain no brackets, so we consume
//     them here as plain code.
//   - lifetimes like 'a or 'static are followed by identifier characters
//     and never have a closing apostrophe in the same word; the apostrophe
//     plus identifier is consumed so it does not enter the string state
//     (which previously caused a 100% false "unterminated string" rate on
//     any .rs file using lifetimes).
//
// handled=false means the caller should fall back to normal string-state
// scanning (e.g. 'abc' — not valid Rust, but treated as a quoted string).
func rustSingleQuoteSpan(content string, i int) (next int, handled bool) {
	n := len(content)
	j := i + 1
	if j >= n {
		return 0, false
	}
	if content[j] == '\\' {
		// Escaped char literal: '\n', '\t', '\u{1F600}'...
		k := j + 1 // past the backslash, at the escape body
		if k < n && content[k] == 'u' && k+1 < n && content[k+1] == '{' {
			for k < n && content[k] != '}' {
				k++
			}
			k++ // past '}'
		} else {
			k++ // past the single escaped character
		}
		if k < n && content[k] == '\'' {
			return k + 1, true // compact close: char literal
		}
		return 0, false
	}
	if isRustIdentChar(content[j]) {
		k := j
		for k < n && isRustIdentChar(content[k]) {
			k++
		}
		if k < n && content[k] == '\'' {
			return 0, false // 'abc' — quoted string, not a lifetime
		}
		return k, true // lifetime 'a / 'static: no closing apostrophe
	}
	return 0, false
}

// isRustIdentChar reports whether c can appear in a Rust identifier/lifetime.
func isRustIdentChar(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
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
