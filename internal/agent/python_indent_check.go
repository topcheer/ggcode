package agent

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const maxIndentScanSize = 512 * 1024

func checkPythonIndentation(filePath, content string) string {
	ext := strings.ToLower(filepathExtSafe(filePath))
	if ext != ".py" && ext != ".pyw" {
		return ""
	}
	if strings.TrimSpace(content) == "" {
		return ""
	}
	if len(content) > maxIndentScanSize {
		debug.Log("py-indent", "skipping indent check for %s: size %d > %d limit", filePath, len(content), maxIndentScanSize)
		return ""
	}

	mixedCount := 0
	mixedLines := []int{}
	lineNum := 0

	depth := 0
	inTriple := ""
	contPrev := false

	for _, rawLine := range strings.Split(content, "\n") {
		lineNum++
		inStringAtStart := inTriple != ""
		depthAtStart := depth
		depth, inTriple = scanPythonLogicalState(rawLine, depth, inTriple)
		contNow := strings.HasSuffix(rawLine, "\\")

		checkable := !inStringAtStart && depthAtStart == 0 && !contPrev
		contPrev = contNow
		if !checkable {
			continue
		}

		stripped := strings.TrimLeft(rawLine, " \t")

		if stripped == "" || strings.HasPrefix(stripped, "#") {
			continue
		}

		indent := rawLine[:len(rawLine)-len(stripped)]
		if indent == "" {
			continue
		}

		hasTab := strings.Contains(indent, "\t")
		hasSpace := strings.Contains(indent, " ")
		if hasTab && hasSpace {
			mixedCount++
			if len(mixedLines) < 3 {
				mixedLines = append(mixedLines, lineNum)
			}
		}
	}

	if mixedCount == 0 {
		return ""
	}

	debug.Log("py-indent", "found %d mixed tab/space line(s) in %s", mixedCount, filePath)

	var msg string
	if len(mixedLines) == 1 {
		msg = fmt.Sprintf("line %d: mixed tabs and spaces in indentation - this can cause TabError/IndentationError in Python 3. Use only spaces (PEP 8 recommends 4 spaces per level).",
			mixedLines[0])
	} else {
		lineStrs := make([]string, len(mixedLines))
		for i, l := range mixedLines {
			lineStrs[i] = fmt.Sprintf("%d", l)
		}
		msg = fmt.Sprintf("lines %s: mixed tabs and spaces in indentation (%d occurrences) - this can cause TabError/IndentationError in Python 3. Use only spaces (PEP 8 recommends 4 spaces per level).",
			strings.Join(lineStrs, ", "), mixedCount)
	}
	return msg
}

func scanPythonLogicalState(line string, depth int, inTriple string) (int, string) {
	i := 0
	for i < len(line) {
		c := line[i]
		if inTriple != "" {
			if strings.HasPrefix(line[i:], inTriple) {
				i += 3
				inTriple = ""
				continue
			}
			i++
			continue
		}
		switch c {
		case '\\':
			i += 2
			continue
		case '\'', '"':
			if strings.HasPrefix(line[i:], "'''") || strings.HasPrefix(line[i:], "\"\"\"") {
				inTriple = line[i : i+3]
				i += 3
				continue
			}
			q := c
			j := i + 1
			for j < len(line) {
				if line[j] == '\\' {
					j += 2
					continue
				}
				if line[j] == q {
					break
				}
				j++
			}
			i = j + 1
			continue
		case '#':
			return depth, inTriple
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth > 0 {
				depth--
			}
		}
		i++
	}
	return depth, inTriple
}

func filepathExtSafe(path string) string {
	for i := len(path) - 1; i >= 0 && path[i] != '/' && path[i] != '\\'; i-- {
		if path[i] == '.' {
			return path[i:]
		}
	}
	return ""
}
