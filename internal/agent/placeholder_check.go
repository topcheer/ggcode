package agent

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

type placeholderPattern struct {
	pattern string
	label   string
	isRegex bool
}

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

var vagueTodoRe = regexp.MustCompile(
	`(?im)^\s*(//|#|--)\s*(TODO|FIXME|HACK|XXX)\s*[:\)]?\s*(implement\s+(this|it|the|logic)|fill\s+in|your\s+code\s+here|add\s+(logic|code|implementation)|not\s+implemented|placeholder|stub\s+here|complete\s+this|coming\s+soon)`)

const maxPlaceholderWarnings = 3

func checkPlaceholderCode(filePath, oldContent, newContent string) []string {
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	ext := filepath.Ext(filePath)
	patterns, ok := placeholderPatternsByExt[ext]
	if !ok {

		return checkVagueTodos(oldContent, newContent)
	}

	if isTestFile(filePath) {
		return nil
	}

	var warnings []string

	oldStripped := stripPlaceholderComments(oldContent, ext)
	newStripped := stripPlaceholderComments(newContent, ext)

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

	warnings = append(warnings, checkVagueTodos(oldContent, newContent)...)

	if len(warnings) > maxPlaceholderWarnings {
		warnings = warnings[:maxPlaceholderWarnings]
	}

	return warnings
}

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

			line = line[closer+3:]
			inDoc = false
		}
		lines[i] = pyStripLineKeepStrings(line, &inDoc, &docQ)
	}
	return strings.Join(lines, "\n")
}

func pyStripLineKeepStrings(line string, inDoc *bool, docQ *rune) string {
	var b strings.Builder
	r := []rune(line)
	n := len(r)
	i := 0
	for i < n {
		c := r[i]
		if c == '#' {
			break
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

func pyScanQuoted(r []rune, i int, inDoc *bool, docQ *rune) (string, int) {
	n := len(r)
	c := r[i]
	if i+2 < n && r[i+1] == c && r[i+2] == c {

		j := i + 3
		for j+2 < n {
			if r[j] == c && r[j+1] == c && r[j+2] == c {
				return "", j + 3
			}
			j++
		}
		*inDoc = true
		*docQ = c
		return "", n
	}

	j := i + 1
	for j < n && r[j] != c {
		j++
	}
	if j < n {
		return string(r[i : j+1]), j + 1
	}
	return string(r[i:]), n
}

func pyFindTripleQuote(line string, q rune) int {
	r := []rune(line)
	for j := 0; j+2 < len(r); j++ {
		if r[j] == q && r[j+1] == q && r[j+2] == q {
			return j
		}
	}
	return -1
}

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

func checkVagueTodos(oldContent, newContent string) []string {
	oldMatches := vagueTodoRe.FindAllString(oldContent, -1)
	newMatches := vagueTodoRe.FindAllString(newContent, -1)

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
