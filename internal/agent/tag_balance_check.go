package agent

// HTML/XML/JSX Tag Balance Detection
//
// Research basis: AI coding agents frequently edit JSX, TSX, Vue, Svelte, and
// HTML templates. A common failure mode is tag imbalance: an agent opens a
// <div> but closes a </span>, or nests tags incorrectly (<div><span></div>).
// Unlike bracket imbalance (caught by delimiter_check.go), tag imbalance is:
//
//   1. NOT caught by go/parser (only applies to Go files)
//   2. NOT caught by bracket balance (tags use <> which aren't brackets)
//   3. NOT always caught at build time (HTML/Vue/Svelte may not have strict
//      compilers; JSX requires a build step that may not run)
//   4. A RUNTIME error in browsers - the page renders incorrectly or crashes
//
// Competitor analysis:
//   - Claude Code: relies on LSP (typescript-language-server) for JSX, but
//     only if the server is running and configured
//   - Cursor: IDE-bound, uses real-time diagnostics
//   - Cline/OpenHands: post-edit build verification (slow, may not run)
//   - Aider: no structural validation for non-Go files
//
// Our approach: lightweight, zero-dependency tag balance check that runs as
// part of the write integrity pipeline. It validates that HTML/XML tags are
// properly balanced (every opening tag has a matching closing tag, properly
// nested). Handles self-closing tags, void elements, and JSX fragments.

import (
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// tagBalanceFileExts are file extensions where tag balance is syntactically
// significant.
var tagBalanceFileExts = map[string]bool{
	".html":   true,
	".htm":    true,
	".xml":    true,
	".xhtml":  true,
	".svg":    true,
	".jsx":    true,
	".tsx":    true,
	".vue":    true,
	".svelte": true,
}

// voidElementsFileExts are extensions of the HTML language family, where
// void elements (link/meta/br/...) legally omit closing tags. XML-family
// files (.xml/.svg) require strict pairing — e.g. RSS `<link>text</link>`
// is a container, not a void element (fix #236).
var voidElementsFileExts = map[string]bool{
	".html": true, ".htm": true, ".xhtml": true,
	".jsx": true, ".tsx": true, ".vue": true, ".svelte": true,
}

// voidElements are HTML elements that don't need closing tags.
// Source: https://html.spec.whatwg.org/multipage/syntax.html#void-elements
var voidElements = map[string]bool{
	"area": true, "base": true, "br": true, "col": true,
	"embed": true, "hr": true, "img": true, "input": true,
	"link": true, "meta": true, "param": true, "source": true,
	"track": true, "wbr": true,
}

// maxTagScanSize limits the content size for tag balance checking.
const maxTagScanSize = 512 * 1024 // 512KB

// checkTagBalance validates that HTML/XML tags are properly balanced in
// markup files. Returns a non-empty warning string if imbalance is detected.
func checkTagBalance(filePath, content string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	if !tagBalanceFileExts[ext] {
		return ""
	}
	if strings.TrimSpace(content) == "" {
		return ""
	}
	if len(content) > maxTagScanSize {
		debug.Log("tag_balance", "skipping %s: size %d > %d", filePath, len(content), maxTagScanSize)
		return ""
	}

	msg := scanTagBalance(content, voidElementsFileExts[ext])
	if msg == "" {
		return ""
	}

	return "[tag imbalance] " + msg + " - this will likely cause a rendering error or build failure."
}

// tagStackEntry tracks an opening tag for balance checking.
type tagStackEntry struct {
	name string
	line int
}

// tagRe matches HTML/XML tags: opening (<div>), closing (</div>),
// self-closing (<br/>), and JSX fragments (<> </>).
// It also captures comments <!-- --> to skip them.
var tagRe = regexp.MustCompile(
	`<!--[\s\S]*?-->` + // comments
		`|<!\[CDATA\[[\s\S]*?\]\]>` + // CDATA
		`|<\/([a-zA-Z][a-zA-Z0-9.-]*)\s*>` + // closing tag: </div>
		`|<([a-zA-Z][a-zA-Z0-9.-]*)(\s[^<>]*?)?\/?>` + // opening or self-closing: <div>, <br/>
		`|<>` + // JSX fragment open
		`|<\/>`, // JSX fragment close
)

// jsxTextExprStrRes match JSX text-level expression containers holding a
// single string literal, e.g. {"</div>"} (fix #277). The string content is
// blanked so a closing-tag-looking sequence inside the expression is not
// treated as a real closing tag. These short anchored patterns cannot chain
// across tags, so a whole-file pass is safe here (unlike attribute values,
// see preprocessTags).
var jsxTextExprStrRes = []*regexp.Regexp{
	regexp.MustCompile(`\{"[^"]*"\}`),
	regexp.MustCompile(`\{'[^']*'\}`),
}

// blankStrLiteralsInJSXTextExpressions blanks string-literal contents inside
// JSX text-level expression containers, preserving offsets so line numbers
// stay accurate.
func blankStrLiteralsInJSXTextExpressions(content string) string {
	out := content
	for _, re := range jsxTextExprStrRes {
		out = re.ReplaceAllStringFunc(out, func(m string) string {
			b := []byte(m)
			for i := 2; i < len(b)-2; i++ { // keep {, quotes and }
				b[i] = ' '
			}
			return string(b)
		})
	}
	return out
}

// isTagNameByte reports whether c can appear in a tag name (per tagRe).
func isTagNameByte(c byte) bool {
	return c == '.' || c == '-' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9')
}

// preprocessTags blanks quoted attribute-value interiors and ={...} JSX
// expression containers inside OPENING TAGS only, preserving offsets so line
// numbers stay accurate (fixes #275 and #277).
//
// This replaces the old whole-file attrValueRe pass, which paired quotes
// across the entire document: an orphan `= "` in body text (e.g.
// `<p>result = "pending</p><div class="x"></div>`) chained into a later
// tag's attribute quotes and blanked real markup, producing both false
// positives ("closing tag no match") and false negatives. Because this pass
// is scoped inside a single tag's angle brackets, no cross-tag chaining is
// possible. It is also quote-aware, so '>' characters inside quoted values
// or expression containers (attr={"</div>"}) no longer terminate the tag
// scan early.
func preprocessTags(content string) string {
	b := []byte(content)
	n := len(b)
	i := 0
	for i < n {
		if b[i] != '<' {
			i++
			continue
		}
		rest := b[i:]
		switch {
		case bytes.HasPrefix(rest, []byte("<!--")):
			if end := bytes.Index(rest, []byte("-->")); end >= 0 {
				i += end + 3
			} else {
				i = n
			}
		case bytes.HasPrefix(rest, []byte("<![CDATA[")):
			if end := bytes.Index(rest, []byte("]]>")); end >= 0 {
				i += end + 3
			} else {
				i = n
			}
		case len(rest) >= 2 && (rest[1] == '/' || rest[1] == '>'):
			// Closing tag or JSX fragment: no attributes to blank.
			i += 2
		case len(rest) >= 2 && isTagNameByte(rest[1]):
			i = blankOpeningTag(b, i)
		default:
			i++ // '<' not starting a tag (e.g. "a < b")
		}
	}
	return string(b)
}

// blankOpeningTag blanks quoted attribute-value interiors and brace
// expression containers within the opening tag starting at b[start] ('<'),
// returning the index just past the tag's closing '>'. Quotes and brace
// nesting are tracked so '>' characters inside them do not terminate the
// tag scan. Offsets are preserved (blanks replace content one-for-one).
func blankOpeningTag(b []byte, start int) int {
	n := len(b)
	i := start + 1
	for i < n && isTagNameByte(b[i]) {
		i++
	}
	for i < n {
		switch b[i] {
		case '>':
			return i + 1
		case '"', '\'':
			quote := b[i]
			j := i + 1
			for j < n && b[j] != quote {
				j++
			}
			if j >= n {
				return n // unterminated tag; nothing more to blank
			}
			for k := i + 1; k < j; k++ {
				b[k] = ' ' // keep surrounding quotes
			}
			i = j + 1
		case '{':
			// Expression container (attribute value or in-tag expression):
			// skip to the matching '}' honoring nesting and nested string
			// literals, then blank the interior so any '<'/'>'/tags inside
			// cannot confuse the subsequent tagRe pass (fix #277).
			j, ok := matchBraceContainer(b, i)
			if !ok {
				return n
			}
			for k := i + 1; k < j; k++ {
				b[k] = ' ' // keep '{' and '}'
			}
			i = j + 1
		default:
			i++
		}
	}
	return n
}

// matchBraceContainer returns the index of the '}' matching the '{' at
// b[start], honoring nested braces and quoted string literals inside the
// container. ok=false if unmatched before EOF.
func matchBraceContainer(b []byte, start int) (end int, ok bool) {
	n := len(b)
	depth := 0
	j := start
	for j < n {
		switch b[j] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return j, true
			}
		case '"', '\'':
			quote := b[j]
			j++
			for j < n && b[j] != quote {
				j++
			}
		}
		j++
	}
	return 0, false
}

// scanTagBalance walks the content looking for tags and validates balance.
// allowVoid enables HTML void-element semantics; XML-family callers must
// pass false for strict pairing.
func scanTagBalance(content string, allowVoid bool) string {
	var stack []tagStackEntry

	// Blank string literals inside JSX text-level expression containers
	// ({"</div>"}) first (fix #277). These anchored patterns cannot chain
	// across tags, so a whole-file pass is safe.
	scanned := blankStrLiteralsInJSXTextExpressions(content)
	// Blank quoted attribute values and expression containers inside opening
	// tags only (fixes #275/#277): an orphan `= "` in body text can no longer
	// pair with a later tag's quotes and blank real markup.
	scanned = preprocessTags(scanned)

	for _, m := range tagRe.FindAllStringSubmatchIndex(scanned, -1) {
		fullMatch := scanned[m[0]:m[1]]

		// Calculate line number for this match.
		line := 1 + strings.Count(content[:m[0]], "\n")

		// Skip comments and CDATA.
		if strings.HasPrefix(fullMatch, "<!--") || strings.HasPrefix(fullMatch, "<![CDATA[") {
			continue
		}

		// JSX fragment open: <>
		if fullMatch == "<>" {
			stack = append(stack, tagStackEntry{name: "<fragment>", line: line})
			continue
		}

		// JSX fragment close: </>
		if fullMatch == "</>" {
			if len(stack) == 0 {
				return fmt.Sprintf("line %d: closing JSX fragment </> with no matching opening <>", line)
			}
			top := stack[len(stack)-1]
			if top.name != "<fragment>" {
				return fmt.Sprintf("line %d: closing JSX fragment </> does not match opening <%s> at line %d", line, top.name, top.line)
			}
			stack = stack[:len(stack)-1]
			continue
		}

		// Closing tag: </div>
		if m[2] >= 0 {
			tagName := strings.ToLower(scanned[m[2]:m[3]])
			if len(stack) == 0 {
				return fmt.Sprintf("line %d: closing tag </%s> with no matching opening tag", line, tagName)
			}
			top := stack[len(stack)-1]
			if top.name != tagName {
				return fmt.Sprintf("line %d: closing tag </%s> does not match opening <%s> at line %d (mismatched nesting)", line, tagName, top.name, top.line)
			}
			stack = stack[:len(stack)-1]
			continue
		}

		// Opening or self-closing tag: <div>, <br/>
		if m[4] >= 0 {
			tagName := strings.ToLower(scanned[m[4]:m[5]])

			// Self-closing: ends with />
			if strings.HasSuffix(fullMatch, "/>") {
				continue // self-closing, no stack push
			}

			// Void element: doesn't need closing tag (HTML family only).
			if allowVoid && voidElements[tagName] {
				continue
			}

			stack = append(stack, tagStackEntry{name: tagName, line: line})
			continue
		}
	}

	// Check for unclosed tags.
	if len(stack) > 0 {
		unclosed := stack[0]
		// Provide context about how many tags are unclosed.
		detail := ""
		if len(stack) > 1 {
			detail = fmt.Sprintf(" (and %d more)", len(stack)-1)
		}
		return fmt.Sprintf("line %d: unclosed <%s> tag — missing closing </%s>%s",
			unclosed.line, unclosed.name, unclosed.name, detail)
	}

	return ""
}
