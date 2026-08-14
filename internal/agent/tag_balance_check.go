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

// attrValueRe matches quoted attribute values (="..." / ='...') so their
// contents can be blanked before tag matching — a `</div>` inside an
// attribute string must not be treated as a real closing tag (fix #236).
var attrValueRe = regexp.MustCompile(`=\s*("[^"]*"|'[^']*')`)

// blankQuotedAttrValues replaces the inner characters of quoted attribute
// values with spaces, preserving offsets so line numbers stay accurate.
func blankQuotedAttrValues(content string) string {
	return attrValueRe.ReplaceAllStringFunc(content, func(m string) string {
		b := []byte(m)
		for i := 2; i < len(b)-1; i++ { // keep '=' and the surrounding quotes
			b[i] = ' '
		}
		return string(b)
	})
}

// scanTagBalance walks the content looking for tags and validates balance.
// allowVoid enables HTML void-element semantics; XML-family callers must
// pass false for strict pairing.
func scanTagBalance(content string, allowVoid bool) string {
	var stack []tagStackEntry

	// Blank quoted attribute values first so closing-tag-looking text inside
	// attribute strings (e.g. <div data-template="</div>">) is not matched.
	scanned := blankQuotedAttrValues(content)

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
