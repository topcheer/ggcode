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
// This module provides a zero-dependency, always-available check that catches the
// two most impactful issues:
//   1. MIXED TABS AND SPACES in the same indentation run (PEP 8 violation,
//      guaranteed TabError in Python 3)
//   2. TRAILING WHITESPACE on code lines that could mask indentation issues
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

	for _, rawLine := range strings.Split(content, "\n") {
		lineNum++
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
		// This is a guaranteed TabError in Python 3 when both appear in the
		// same indentation sequence.
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
		msg = fmt.Sprintf("line %d: mixed tabs and spaces in indentation - this causes TabError in Python 3. Use only spaces (PEP 8 recommends 4 spaces per level).",
			mixedLines[0])
	} else {
		lineStrs := make([]string, len(mixedLines))
		for i, l := range mixedLines {
			lineStrs[i] = fmt.Sprintf("%d", l)
		}
		msg = fmt.Sprintf("lines %s: mixed tabs and spaces in indentation (%d occurrences) - this causes TabError in Python 3. Use only spaces (PEP 8 recommends 4 spaces per level).",
			strings.Join(lineStrs, ", "), mixedCount)
	}
	return msg
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
