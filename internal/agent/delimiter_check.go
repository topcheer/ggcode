package agent

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const maxDelimiterScanSize = 1 << 20

func checkDelimiterBalance(filePath, content string) string {
	if !shouldCheckDelimiters(filePath) {
		return ""
	}
	if strings.TrimSpace(content) == "" {
		return ""
	}

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

type commentStyle struct {
	lineComments []string
	blockOpen    string
	blockClose   string
	tripleQuotes bool
	rust         bool
	jsLike       bool
	heredoc      bool
}

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

		return commentStyle{
			lineComments: []string{"//"},
			blockOpen:    "/*",
			blockClose:   "*/",
			jsLike:       true,
		}
	default:

		return commentStyle{
			lineComments: []string{"//"},
			blockOpen:    "/*",
			blockClose:   "*/",
		}
	}
}

type delimState int

const (
	dsCode = iota
	dsStringSingle
	dsStringDouble
	dsStringBack
	dsLineComment
	dsBlockComment
	dsTripleDouble
	dsTripleSingle
	dsRegex
	dsRegexClass
	dsHeredoc
)

type openBracket struct {
	char byte
	line int
}

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

func (s *delimScanner) handleNewline() {
	s.line++
	if s.state == dsLineComment {
		s.state = dsCode
	}
	if s.state == dsRegex || s.state == dsRegexClass {

		s.state = dsCode
	}
	if s.state == dsCode && s.pendingHeredoc != "" {

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

func (s *delimScanner) stepCode() string {
	if handled, diag := s.openCommentOrTriple(); handled || diag != "" {
		return diag
	}
	if handled, diag := s.openLiteral(); handled || diag != "" {
		return diag
	}
	return s.trackBracket()
}

func (s *delimScanner) openCommentOrTriple() (handled bool, diag string) {
	n := len(s.content)

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

	if s.style.blockOpen != "" && s.i+1 < n && s.content[s.i:s.i+len(s.style.blockOpen)] == s.style.blockOpen {
		s.state = dsBlockComment
		s.i += len(s.style.blockOpen)
		return true, ""
	}

	for _, lc := range s.style.lineComments {
		if s.i+len(lc) <= n && s.content[s.i:s.i+len(lc)] == lc {
			s.state = dsLineComment
			s.i += len(lc)
			return true, ""
		}
	}
	return false, ""
}

func (s *delimScanner) openLiteral() (handled bool, diag string) {
	c := s.content[s.i]
	n := len(s.content)

	if s.style.heredoc && c == '<' && s.i+1 < n && s.content[s.i+1] == '<' {
		if term, next, ok := rubyHeredocStart(s.content, s.i); ok {
			s.pendingHeredoc = term
			s.i = next
			return true, ""
		}
	}

	if s.style.jsLike && c == '/' && regexMayStart(s.content, s.i) {
		s.state = dsRegex
		s.i++
		return true, ""
	}

	if s.style.rust && c == 'r' && s.i+2 < n && s.content[s.i+1] == '"' {
		if end := strings.IndexByte(s.content[s.i+2:], '"'); end >= 0 {
			s.line += strings.Count(s.content[s.i:s.i+2+end+1], "\n")
			s.i += 2 + end + 1
			return true, ""
		}
		return true, fmt.Sprintf("line %d: unterminated raw string literal - missing closing \"", s.line)
	}
	if s.style.rust && c == 'r' && s.i+1 < n && s.content[s.i+1] == '#' {

		k := 1
		for s.i+1+k < n && s.content[s.i+1+k] == '#' {
			k++
		}
		if s.i+1+k < n && s.content[s.i+1+k] == '"' {
			start := s.i + 1 + k
			if end, ok := indexRawStringClose(s.content[start+1:], k); ok {
				closed := start + 1 + end + 1 + k
				s.line += strings.Count(s.content[s.i:closed], "\n")
				s.i = closed
				return true, ""
			}
			return true, fmt.Sprintf("line %d: unterminated raw string literal - missing closing quote followed by %d '#'", s.line, k)
		}

	}

	switch c {
	case '\'':

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

func (s *delimScanner) stepLiteral() {
	c := s.content[s.i]
	n := len(s.content)
	switch s.state {
	case dsStringSingle:
		if c == '\\' {
			s.i += 2
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
			s.i += 2
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

		s.i++

	case dsLineComment:

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

	}
	return ""
}

func (s *delimScanner) unclosedBracketDiagnostic() string {
	if len(s.stack) == 0 {
		return ""
	}
	unclosed := s.stack[0]
	if len(s.stack) > 1 {
		return fmt.Sprintf("line %d: unclosed '%s' — missing closing delimiter (%d unclosed total)", unclosed.line, bracketName(unclosed.char), len(s.stack))
	}
	return fmt.Sprintf("line %d: unclosed '%s' — missing closing delimiter", unclosed.line, bracketName(unclosed.char))
}

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
		return true
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

func isRegexWordByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func rubyHeredocStart(content string, i int) (term string, next int, ok bool) {
	n := len(content)
	j := i + 2
	if j >= n {
		return "", 0, false
	}
	if content[j] == '~' || content[j] == '-' {
		j++
	}
	if j < n && (content[j] == '"' || content[j] == '\'') {
		return rubyHeredocQuotedID(content, j, i)
	}
	return rubyHeredocBareID(content, j, i)
}

func rubyHeredocQuotedID(content string, j, i int) (string, int, bool) {
	n := len(content)
	q := content[j]
	k := j + 1
	for k < n && content[k] != q && content[k] != '\n' {
		k++
	}
	if k >= n || content[k] != q || k == j+1 {
		return "", 0, false
	}
	if !heredocStartAllowedAfter(content, i) {
		return "", 0, false
	}
	return content[j+1 : k], k + 1, true
}

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

func heredocStartAllowedAfter(content string, i int) bool {
	p := i - 1
	for p >= 0 && (content[p] == ' ' || content[p] == '\t') {
		p--
	}
	if p < 0 {
		return true
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
		return 0, false
	}
	return end, true
}

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

func rustSingleQuoteSpan(content string, i int) (next int, handled bool) {
	n := len(content)
	j := i + 1
	if j >= n {
		return 0, false
	}
	if content[j] == '\\' {

		k := j + 1
		if k < n && content[k] == 'u' && k+1 < n && content[k+1] == '{' {
			for k < n && content[k] != '}' {
				k++
			}
			k++
		} else {
			k++
		}
		if k < n && content[k] == '\'' {
			return k + 1, true
		}
		return 0, false
	}
	if isRustIdentChar(content[j]) {
		k := j
		for k < n && isRustIdentChar(content[k]) {
			k++
		}
		if k < n && content[k] == '\'' {
			return 0, false
		}
		return k, true
	}
	return 0, false
}

func isRustIdentChar(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func isMatchingBracket(open, close byte) bool {
	return (open == '(' && close == ')') ||
		(open == '{' && close == '}') ||
		(open == '[' && close == ']')
}

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
