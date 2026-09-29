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
	jsLike       bool     // JS/TS family: '/' can open a regex literal (fix #538)
	heredoc      bool     // Ruby: <<~ID / <<ID heredoc bodies are string data (fix #538)
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
			heredoc:      true,
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
	case ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".mts", ".cts":
		// C-like languages plus regex literals: after an expression-start
		// context (operator, '(', ',', '=', keywords) a '/' opens a regex
		// literal whose body — including character classes — must not feed
		// the string or bracket states (fix #538).
		return commentStyle{
			lineComments: []string{"//"},
			blockOpen:    "/*",
			blockClose:   "*/",
			jsLike:       true,
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
type delimState int

const (
	dsCode         = iota
	dsStringSingle // '...'
	dsStringDouble // "..."
	dsStringBack   // `...`
	dsLineComment  // //... or #...
	dsBlockComment // /* ... */
	dsTripleDouble // """ ... """
	dsTripleSingle // ''' ... '''
	dsRegex        // /.../ (JS regex literal, fix #538)
	dsRegexClass   // [...] inside a JS regex literal
	dsHeredoc      // Ruby heredoc body, terminator tracked separately
)

// openBracket tracks an opening delimiter and its line number for diagnostics.
type openBracket struct {
	char byte
	line int
}

// scanDelimiters scans content for unbalanced (), {}, [] outside of strings and
// comments. Returns an empty string if balanced, or a diagnostic message.
// delimScanner carries the mutable state of the delimiter state machine so
// each transition group (newline handling, code-state dispatch, literal
// scanning, EOF diagnostics) is a small independent method. The logic is
// identical to the pre-refactor monolith (fix #237/#276/#538/#605 behavior
// preserved verbatim); only the shape changed.
type delimScanner struct {
	content        string
	style          commentStyle
	i              int
	line           int
	state          delimState
	stack          []openBracket
	pendingHeredoc string
	heredocTerm    string
}

// scanDelimiters scans content for unbalanced (), {}, [] outside of strings and
// comments. Returns an empty string if balanced, or a diagnostic message.
func scanDelimiters(content string, style commentStyle) string {
	s := &delimScanner{content: content, style: style, state: dsCode, line: 1}
	for s.i < len(content) {
		if content[s.i] == '\n' {
			s.handleNewline()
			continue
		}
		if s.state == dsCode {
			if diag := s.stepCode(); diag != "" {
				return diag
			}
			continue
		}
		s.stepLiteral()
	}
	if diag := s.unterminatedDiagnostic(); diag != "" {
		return diag
	}
	return s.unclosedBracketDiagnostic()
}

// handleNewline consumes the '\n' at s.i, applying end-of-line state resets and
// heredoc body entry/exit transitions.
func (s *delimScanner) handleNewline() {
	s.line++
	if s.state == dsLineComment {
		s.state = dsCode
	}
	if s.state == dsRegex || s.state == dsRegexClass {
		// A JS regex literal cannot span lines, so a newline inside one
		// means the regex-context heuristic misfired (it was really a
		// division, e.g. "a /= b"). Contain the damage to that line:
		// resume code state without reporting.
		s.state = dsCode
	}
	if s.state == dsCode && s.pendingHeredoc != "" {
		// A heredoc start was seen earlier on the line that just ended;
		// its body begins on this next line (fix #538).
		s.heredocTerm = s.pendingHeredoc
		s.pendingHeredoc = ""
		s.state = dsHeredoc
		s.i++
		return
	}
	if s.state == dsHeredoc {
		if end, ok := heredocTerminatorEnd(s.content, s.i+1, s.heredocTerm); ok {
			s.i = end
			s.state = dsCode
			return
		}
	}
	s.i++
}

// stepCode runs one dsCode transition: it first tries to open a comment,
// triple-quoted string or literal region, and otherwise tracks brackets.
// Returns a diagnostic when scanning must stop.
func (s *delimScanner) stepCode() string {
	if handled, diag := s.openCommentOrTriple(); handled || diag != "" {
		return diag
	}
	if handled, diag := s.openLiteral(); handled || diag != "" {
		return diag
	}
	return s.trackBracket()
}

// openCommentOrTriple consumes a Python triple-quoted string opener, a block
// comment opener, or a line comment marker at s.i, if one starts there.
func (s *delimScanner) openCommentOrTriple() (handled bool, diag string) {
	n := len(s.content)
	// --- Triple-quoted strings (Python) ---
	if s.style.tripleQuotes && s.i+2 < n {
		switch string(s.content[s.i : s.i+3]) {
		case `"""`:
			s.state = dsTripleDouble
			s.i += 3
			return true, ""
		case `'''`:
			s.state = dsTripleSingle
			s.i += 3
			return true, ""
		}
	}

	// --- Block comment open ---
	if s.style.blockOpen != "" && s.i+1 < n && s.content[s.i:s.i+len(s.style.blockOpen)] == s.style.blockOpen {
		s.state = dsBlockComment
		s.i += len(s.style.blockOpen)
		return true, ""
	}

	// --- Line comment ---
	for _, lc := range s.style.lineComments {
		if s.i+len(lc) <= n && s.content[s.i:s.i+len(lc)] == lc {
			s.state = dsLineComment
			s.i += len(lc)
			return true, ""
		}
	}
	return false, ""
}

// openLiteral consumes the start of a Ruby heredoc, JS regex literal, Rust raw
// string, or plain string literal at s.i, if one starts there.
func (s *delimScanner) openLiteral() (handled bool, diag string) {
	c := s.content[s.i]
	n := len(s.content)

	// --- Ruby heredoc start (fix #538): <<~ID, <<-ID, <<ID, <<"ID", <<'ID' ---
	// The heredoc body is string data, so bare brackets inside it must
	// not enter the balance stack. The body only begins on the NEXT
	// line, so just remember the terminator here and keep scanning the
	// rest of the start line as code (e.g. in foo(<<~SQL.strip) the
	// paren still closes on the same line).
	if s.style.heredoc && c == '<' && s.i+1 < n && s.content[s.i+1] == '<' {
		if term, next, ok := rubyHeredocStart(s.content, s.i); ok {
			s.pendingHeredoc = term
			s.i = next
			return true, ""
		}
	}

	// --- JS regex literal vs division (fix #538) ---
	// Decided from the previous significant character: after operators,
	// opening brackets, separators or keywords a '/' opens a regex. The
	// regex body (and its character classes) is consumed wholesale so
	// patterns like /['"]+/ or /[(]/ no longer derange the string and
	// bracket states. Note: '/' after ')' is treated as division (the
	// common (a+b)/2 case); "if (x) /re/" misreads are contained by the
	// newline fallback in handleNewline.
	if s.style.jsLike && c == '/' && regexMayStart(s.content, s.i) {
		s.state = dsRegex
		s.i++
		return true, ""
	}

	// --- Rust raw strings r"...", r#"..."#, r##"..."## ... (fix #237, #276) ---
	// Rust allows an arbitrary number of hashes; the string closes only
	// at a '"' followed by the same number of hashes. Content may itself
	// contain "#, which is why counting k is required.
	if s.style.rust && c == 'r' && s.i+2 < n && s.content[s.i+1] == '"' {
		if end := strings.IndexByte(s.content[s.i+2:], '"'); end >= 0 {
			s.line += strings.Count(s.content[s.i:s.i+2+end+1], "\n")
			s.i += 2 + end + 1
			return true, ""
		}
		return true, fmt.Sprintf("line %d: unterminated raw string literal - missing closing \"", s.line)
	}
	if s.style.rust && c == 'r' && s.i+1 < n && s.content[s.i+1] == '#' {
		// Count consecutive hashes after 'r'.
		k := 1
		for s.i+1+k < n && s.content[s.i+1+k] == '#' {
			k++
		}
		if s.i+1+k < n && s.content[s.i+1+k] == '"' {
			start := s.i + 1 + k // index of opening '"'
			if end, ok := indexRawStringClose(s.content[start+1:], k); ok {
				closed := start + 1 + end + 1 + k // past '"' and k hashes
				s.line += strings.Count(s.content[s.i:closed], "\n")
				s.i = closed
				return true, ""
			}
			return true, fmt.Sprintf("line %d: unterminated raw string literal - missing closing quote followed by %d '#'", s.line, k)
		}
		// 'r' followed by hashes but no quote: not a raw string (e.g.
		// an identifier); fall through to normal handling.
	}

	// --- String starts ---
	switch c {
	case '\'':
		// Rust lifetime vs char literal (fix #237): lifetimes ('a,
		// 'static) never close and must not enter the string state;
		// char literals ('x', '\n') close compactly and contain no
		// brackets, so consuming them as code is safe.
		if s.style.rust {
			if next, spanHandled := rustSingleQuoteSpan(s.content, s.i); spanHandled {
				s.i = next
				return true, ""
			}
		}
		s.state = dsStringSingle
		s.i++
		return true, ""
	case '"':
		s.state = dsStringDouble
		s.i++
		return true, ""
	case '`':
		s.state = dsStringBack
		s.i++
		return true, ""
	}
	return false, ""
}

// trackBracket pushes or pops the bracket stack for the code byte at s.i.
func (s *delimScanner) trackBracket() string {
	switch c := s.content[s.i]; c {
	case '(', '{', '[':
		s.stack = append(s.stack, openBracket{char: c, line: s.line})
	case ')', '}', ']':
		if len(s.stack) == 0 {
			return fmt.Sprintf("line %d: unexpected '%c' with no matching opening delimiter", s.line, c)
		}
		top := s.stack[len(s.stack)-1]
		if !isMatchingBracket(top.char, c) {
			return fmt.Sprintf("line %d: '%c' does not match '%c' opened at line %d", s.line, c, top.char, top.line)
		}
		s.stack = s.stack[:len(s.stack)-1]
	}
	s.i++
	return ""
}

// stepLiteral advances one byte inside a string/regex/comment/heredoc region.
// Newlines never reach here — handleNewline consumes them before the state
// switch runs — which is why the old triple-quote '\n' line-counting branches
// (dead even before this refactor) are gone.
func (s *delimScanner) stepLiteral() {
	c := s.content[s.i]
	n := len(s.content)
	switch s.state {
	case dsStringSingle:
		if c == '\\' {
			s.i += 2 // skip escaped char
			return
		}
		if c == '\'' {
			s.state = dsCode
		}
		s.i++

	case dsStringDouble:
		if c == '\\' {
			s.i += 2
			return
		}
		if c == '"' {
			s.state = dsCode
		}
		s.i++

	case dsStringBack:
		if c == '\\' {
			s.i += 2
			return
		}
		if c == '`' {
			s.state = dsCode
		}
		s.i++

	case dsRegex:
		if c == '\\' {
			s.i += 2 // skip escaped char
			return
		}
		if c == '[' {
			s.state = dsRegexClass
			s.i++
			return
		}
		if c == '/' {
			s.state = dsCode
		}
		// Newlines are handled by handleNewline (containment).
		s.i++

	case dsRegexClass:
		if c == '\\' {
			s.i += 2
			return
		}
		if c == ']' {
			s.state = dsRegex
		}
		s.i++

	case dsHeredoc:
		// Everything until the terminator line is string data; terminator
		// detection happens in handleNewline.
		s.i++

	case dsLineComment:
		// Consumed by handleNewline.
		s.i++

	case dsBlockComment:
		if s.style.blockClose != "" && s.i+len(s.style.blockClose) <= n && s.content[s.i:s.i+len(s.style.blockClose)] == s.style.blockClose {
			s.state = dsCode
			s.i += len(s.style.blockClose)
			return
		}
		s.i++

	case dsTripleDouble:
		if s.i+2 < n && s.content[s.i:s.i+3] == `"""` {
			s.state = dsCode
			s.i += 3
			return
		}
		s.i++

	case dsTripleSingle:
		if s.i+2 < n && s.content[s.i:s.i+3] == `'''` {
			s.state = dsCode
			s.i += 3
			return
		}
		s.i++
	}
}

// unterminatedDiagnostic reports a syntax error when the file ends inside a
// string, comment, or heredoc region. An agent edit that leaves a string or
// comment unterminated causes a syntax error in every language — a very common
// failure mode for partial edits.
func (s *delimScanner) unterminatedDiagnostic() string {
	switch s.state {
	case dsStringSingle:
		return fmt.Sprintf("line %d: unterminated single-quoted string - missing closing '", unclosedStringLine(s.content, s.i))
	case dsStringDouble:
		return fmt.Sprintf("line %d: unterminated double-quoted string - missing closing double-quote", unclosedStringLine(s.content, s.i))
	case dsStringBack:
		return fmt.Sprintf("line %d: unterminated template literal - missing closing backtick", unclosedStringLine(s.content, s.i))
	case dsBlockComment:
		return fmt.Sprintf("line %d: unterminated block comment - missing closing */", unclosedStringLine(s.content, s.i))
	case dsTripleDouble:
		return fmt.Sprintf("line %d: unterminated triple-quoted string - missing closing triple double-quote", unclosedStringLine(s.content, s.i))
	case dsTripleSingle:
		return fmt.Sprintf("line %d: unterminated triple-quoted string - missing closing '''", unclosedStringLine(s.content, s.i))
	case dsHeredoc:
		return fmt.Sprintf("line %d: unterminated heredoc - missing terminator %s", unclosedStringLine(s.content, s.i), s.heredocTerm)
		// dsRegex/dsRegexClass are deliberately NOT reported at EOF: that
		// can also mean the heuristic read a division as a regex start
		// (e.g. "x /= 2" on the last line); staying silent keeps this
		// detector false-positive-averse (fix #538).
	}
	return ""
}

// unclosedBracketDiagnostic reports the outermost unclosed opening delimiter.
// #605 G4: include the count so a 1→2 worsening write produces a DIFFERENT
// message than the pre-existing single imbalance — the delta gate compares
// messages to separate "untouched" from "introduced/worsened".
func (s *delimScanner) unclosedBracketDiagnostic() string {
	if len(s.stack) == 0 {
		return ""
	}
	unclosed := s.stack[0] // outermost unclosed
	if len(s.stack) > 1 {
		return fmt.Sprintf("line %d: unclosed '%s' — missing closing delimiter (%d unclosed total)", unclosed.line, bracketName(unclosed.char), len(s.stack))
	}
	return fmt.Sprintf("line %d: unclosed '%s' — missing closing delimiter", unclosed.line, bracketName(unclosed.char))
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

// regexMayStart reports whether the '/' at content[i] begins a regular
// expression literal rather than a division operator, judged from the last
// significant character before it (fix #538). A regex may start where an
// expression is expected: after operators, opening brackets, separators, or
// keywords like return/typeof/case. After an identifier, a number, or a
// closing bracket it is division.
func regexMayStart(content string, i int) bool {
	j := i - 1
	for j >= 0 {
		switch content[j] {
		case ' ', '\t', '\r', '\n':
			j--
			continue
		}
		break
	}
	if j < 0 {
		return true // start of file
	}
	switch content[j] {
	case '(', ',', '=', ':', '[', '!', '&', '|', '?', '{', '}', ';',
		'+', '-', '*', '%', '<', '>', '~', '^':
		return true
	}
	if isRegexWordByte(content[j]) {
		k := j
		for k >= 0 && isRegexWordByte(content[k]) {
			k--
		}
		switch content[k+1 : j+1] {
		case "return", "typeof", "instanceof", "in", "of", "case", "do",
			"else", "new", "delete", "void", "yield", "await", "throw":
			return true
		}
	}
	return false
}

// isRegexWordByte reports whether b can appear in a JS identifier or number.
func isRegexWordByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// rubyHeredocStart parses a Ruby heredoc start at content[i] (pointing at the
// first '<' of "<<"). It returns the terminator identifier, the index just
// past the heredoc start token, and ok=false when this is not a heredoc but a
// plain << shift. Guards against misdetection (fix #538):
//   - the token must be <<, <<~, <<-, <<"ID" or <<'ID'
//   - a bare terminator must start with an uppercase letter (SQL, EOS...) —
//     shift operands are lowercase variables in practice
//   - the character before "<<" must not close an expression (')', ']', '}',
//     quote) or be a digit: "x << Y" is a shift, "puts <<SQL" is a heredoc
func rubyHeredocStart(content string, i int) (term string, next int, ok bool) {
	n := len(content)
	j := i + 2 // past "<<"
	if j >= n {
		return "", 0, false
	}
	if content[j] == '~' || content[j] == '-' {
		j++ // squiggly / indentable heredoc
	}
	if j < n && (content[j] == '"' || content[j] == '\'') {
		return rubyHeredocQuotedID(content, j, i)
	}
	return rubyHeredocBareID(content, j, i)
}

// rubyHeredocQuotedID parses <<"ID" / <<'ID' (after any ~/-) starting at the
// opening quote content[j]; i is the index of the first '<' for the
// preceded-by guard (see rubyHeredocStart).
func rubyHeredocQuotedID(content string, j, i int) (string, int, bool) {
	n := len(content)
	q := content[j]
	k := j + 1
	for k < n && content[k] != q && content[k] != '\n' {
		k++
	}
	if k >= n || content[k] != q || k == j+1 {
		return "", 0, false // unterminated or empty quoted ID
	}
	if !heredocStartAllowedAfter(content, i) {
		return "", 0, false
	}
	return content[j+1 : k], k + 1, true
}

// rubyHeredocBareID parses a bare terminator identifier (after any ~/-) at
// content[j]. Only identifiers starting with an uppercase letter count as
// heredoc terminators (SQL, EOS...): a lowercase operand is a shift in
// practice. i is the index of the first '<' for the preceded-by guard.
func rubyHeredocBareID(content string, j, i int) (string, int, bool) {
	n := len(content)
	s := j
	for j < n && isRegexWordByte(content[j]) {
		j++
	}
	term := content[s:j]
	if term == "" || term[0] < 'A' || term[0] > 'Z' {
		return "", 0, false
	}
	if !heredocStartAllowedAfter(content, i) {
		return "", 0, false
	}
	return term, j, true
}

// heredocStartAllowedAfter reports whether a heredoc start may legally begin
// at content[i]: the previous significant character must not close an
// expression (')', ']', '}', quote) or be a digit — "x << Y" and "1 << n" are
// shifts, "puts <<SQL" is a heredoc.
func heredocStartAllowedAfter(content string, i int) bool {
	p := i - 1
	for p >= 0 && (content[p] == ' ' || content[p] == '\t') {
		p--
	}
	if p < 0 {
		return true // start of file
	}
	switch content[p] {
	case ')', ']', '}', '"', '\'', '`':
		return false
	}
	if content[p] >= '0' && content[p] <= '9' {
		return false
	}
	return true
}

// heredocTerminatorEnd reports whether the line starting at content[pos] is
// (optional whitespace +) the heredoc terminator term, returning the index
// just past the terminator. Indentation is allowed for <<~/<<- forms and
// tolerated for << to stay lenient.
func heredocTerminatorEnd(content string, pos int, term string) (int, bool) {
	n := len(content)
	j := pos
	for j < n && (content[j] == ' ' || content[j] == '\t') {
		j++
	}
	if !strings.HasPrefix(content[j:], term) {
		return 0, false
	}
	end := j + len(term)
	if end < n && content[end] != '\n' {
		return 0, false // the terminator must be alone on its line
	}
	return end, true
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
