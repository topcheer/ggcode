package agent

// Python Indentation Consistency Validation
//
// Research basis: Python is unique among major languages in using indentation
// (whitespace) as a syntactic block delimiter. Mixing tabs and spaces, or using
// inconsistent indentation within the same block, causes IndentationError or
// TabError at runtime - a silent failure that only surfaces when the code runs.
//
// The SWE-agent paper and follow-up studies show that indentation errors are
// among the top-5 failure modes for LLM-generated Python code. Agents frequently:
//   - Mix tabs and spaces when editing existing code (copy-paste from different sources)
//   - Use wrong indentation level after a partial edit
//   - Introduce trailing whitespace that changes block structure
//
// Competitor analysis:
//   - Claude Code: relies on LSP (pyright/pylsp) - not always available in CLI
//   - Cursor: in-process diagnostics via the Python language server
//   - Aider: no Python indentation validation
//   - OpenHands/Cline: post-edit test execution catches it (slow feedback loop)
//
// This module provides a zero-dependency, always-available check that catches
// the most impactful issue:
//   1. MIXED TABS AND SPACES in the same indentation run (PEP 8 violation,
//      can cause TabError in Python 3)
//
// (#1865 case 3: the header used to also advertise TRAILING WHITESPACE
// detection, which was never implemented - dead doc removed.)
//
// Lines inside open brackets (continuation lines, where indentation has no
// syntactic meaning) and lines inside triple-quoted strings (string data,
// not indentation) are excluded (#1865 case 2).
//
// The check runs after successful file writes on .py/.pyw files, is <1ms for
// typical files, and is non-blocking.

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// maxIndentScanSize limits the check to avoid overhead on very large files.
const maxIndentScanSize = 512 * 1024 // 512KB

// checkPythonIndentation validates Python indentation consistency after a write.
// Returns a non-empty warning string if mixed tabs/spaces are detected.
// Returns "" for non-Python files, empty content, or files with no issues.
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
	// #1865 case 2: bracket depth and triple-quote state, carried across
	// lines, separate CODE indentation (significant) from continuation
	// lines inside brackets and string DATA inside triple quotes (neither
	// is indentation). A trailing backslash also makes the next line a
	// continuation.
	depth := 0
	inTriple := ""
	contPrev := false

	for _, rawLine := range strings.Split(content, "\n") {
		lineNum++
		inStringAtStart := inTriple != ""
		depthAtStart := depth
		depth, inTriple = scanPythonLogicalState(rawLine, depth, inTriple)
		contNow := strings.HasSuffix(rawLine, "\\")

		// Indent is checkable only on lines that open a logical line of
		// their own: not string data, not a bracket/backslash continuation.
		checkable := !inStringAtStart && depthAtStart == 0 && !contPrev
		contPrev = contNow
		if !checkable {
			continue
		}

		stripped := strings.TrimLeft(rawLine, " \t")

		// Skip blank lines and comment-only lines (no indentation significance).
		if stripped == "" || strings.HasPrefix(stripped, "#") {
			continue
		}

		// Extract the leading whitespace (indentation run).
		indent := rawLine[:len(rawLine)-len(stripped)]
		if indent == "" {
			continue
		}

		// Check for mixed tabs and spaces in the indentation run.
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

// scanPythonLogicalState carries bracket depth and triple-quote state
// across the lines of a Python source (#1865 case 2). Single-line strings
// are consumed so brackets inside them do not count; a '#' outside a
// string ends the logical scan for the line; '\\' escapes the next
// character. Depth never goes below zero (tolerates unbalanced closers
// in damaged files).
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

// filepathExtSafe returns the file extension without importing filepath in
// files that already import it via another path. Uses strings only.
func filepathExtSafe(path string) string {
	for i := len(path) - 1; i >= 0 && path[i] != '/' && path[i] != '\\'; i-- {
		if path[i] == '.' {
			return path[i:]
		}
	}
	return ""
}
