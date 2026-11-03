package agent

import (
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

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

var voidElementsFileExts = map[string]bool{
	".html": true, ".htm": true, ".xhtml": true,
	".jsx": true, ".tsx": true, ".vue": true, ".svelte": true,
}

var voidElements = map[string]bool{
	"area": true, "base": true, "br": true, "col": true,
	"embed": true, "hr": true, "img": true, "input": true,
	"link": true, "meta": true, "param": true, "source": true,
	"track": true, "wbr": true,
}

var htmlOptionalEndTagFileExts = map[string]bool{
	".html": true, ".htm": true,
}

var optionalEndTagElements = map[string]bool{
	"li": true, "dt": true, "dd": true, "p": true,
	"rt": true, "rp": true, "option": true, "optgroup": true,
	"colgroup": true, "caption": true, "thead": true, "tbody": true,
	"tfoot": true, "tr": true, "td": true, "th": true,
}

var startTagImplicitCloses = map[string]map[string]bool{
	"li":       {"li": true},
	"dt":       {"dt": true, "dd": true},
	"dd":       {"dt": true, "dd": true},
	"p":        {"p": true},
	"option":   {"option": true},
	"optgroup": {"optgroup": true, "option": true},
	"rt":       {"rt": true, "rp": true},
	"rp":       {"rt": true, "rp": true},
	"td":       {"td": true, "th": true},
	"th":       {"td": true, "th": true},
	"tr":       {"tr": true, "td": true, "th": true},
	"thead":    {"thead": true},
	"tbody":    {"thead": true, "tbody": true, "tfoot": true, "tr": true, "td": true, "th": true},
	"tfoot":    {"thead": true, "tbody": true, "tfoot": true, "tr": true, "td": true, "th": true},
	"caption":  {"caption": true},
}

const maxTagScanSize = 512 * 1024

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

	msg := scanTagBalance(content, voidElementsFileExts[ext], htmlOptionalEndTagFileExts[ext])
	if msg == "" {
		return ""
	}

	return "[tag imbalance] " + msg + " - this will likely cause a rendering error or build failure."
}

type tagStackEntry struct {
	name string
	line int
}

var tagRe = regexp.MustCompile(
	`<!--[\s\S]*?-->` +
		`|<!\[CDATA\[[\s\S]*?\]\]>` +
		`|<\/([a-zA-Z][a-zA-Z0-9.-]*)\s*>` +
		`|<([a-zA-Z][a-zA-Z0-9.-]*)(\s[^<>]*?)?\/?>` +
		`|<>` +
		`|<\/>`,
)

var jsxTextExprStrRes = []*regexp.Regexp{
	regexp.MustCompile(`\{"[^"]*"\}`),
	regexp.MustCompile(`\{'[^']*'\}`),
}

func blankStrLiteralsInJSXTextExpressions(content string) string {
	out := content
	for _, re := range jsxTextExprStrRes {
		out = re.ReplaceAllStringFunc(out, func(m string) string {
			b := []byte(m)
			for i := 2; i < len(b)-2; i++ {
				b[i] = ' '
			}
			return string(b)
		})
	}
	return out
}

func isTagNameByte(c byte) bool {
	return c == '.' || c == '-' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9')
}

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

			i += 2
		case len(rest) >= 2 && isTagNameByte(rest[1]):
			i = blankOpeningTag(b, i)
		default:
			i++
		}
	}
	return string(b)
}

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
				return n
			}
			for k := i + 1; k < j; k++ {
				b[k] = ' '
			}
			i = j + 1
		case '{':

			j, ok := matchBraceContainer(b, i)
			if !ok {
				return n
			}
			for k := i + 1; k < j; k++ {
				b[k] = ' '
			}
			i = j + 1
		default:
			i++
		}
	}
	return n
}

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

func scanTagBalance(content string, allowVoid, htmlOptional bool) string {
	var stack []tagStackEntry

	scanned := blankStrLiteralsInJSXTextExpressions(content)

	scanned = preprocessTags(scanned)

	for _, m := range tagRe.FindAllStringSubmatchIndex(scanned, -1) {
		fullMatch := scanned[m[0]:m[1]]

		line := 1 + strings.Count(content[:m[0]], "\n")

		if strings.HasPrefix(fullMatch, "<!--") || strings.HasPrefix(fullMatch, "<![CDATA[") {
			continue
		}

		if fullMatch == "<>" {
			stack = append(stack, tagStackEntry{name: "<fragment>", line: line})
			continue
		}

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

		if m[2] >= 0 {
			tagName := strings.ToLower(scanned[m[2]:m[3]])

			if htmlOptional {
				for len(stack) > 0 && stack[len(stack)-1].name != tagName &&
					optionalEndTagElements[stack[len(stack)-1].name] {
					stack = stack[:len(stack)-1]
				}
			}
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

		if m[4] >= 0 {
			tagName := strings.ToLower(scanned[m[4]:m[5]])

			if strings.HasSuffix(fullMatch, "/>") {
				continue
			}

			if allowVoid && voidElements[tagName] {
				continue
			}

			if htmlOptional {
				if closed := startTagImplicitCloses[tagName]; len(closed) > 0 {
					for len(stack) > 0 && closed[stack[len(stack)-1].name] {
						stack = stack[:len(stack)-1]
					}
				}
			}

			stack = append(stack, tagStackEntry{name: tagName, line: line})
			continue
		}
	}

	if htmlOptional {
		for len(stack) > 0 && optionalEndTagElements[stack[len(stack)-1].name] {
			stack = stack[:len(stack)-1]
		}
	}

	if len(stack) > 0 {
		unclosed := stack[0]

		detail := ""
		if len(stack) > 1 {
			detail = fmt.Sprintf(" (and %d more)", len(stack)-1)
		}
		return fmt.Sprintf("line %d: unclosed <%s> tag — missing closing </%s>%s",
			unclosed.line, unclosed.name, unclosed.name, detail)
	}

	return ""
}
