package tool

import (
	"fmt"
	"strings"
)

const maxOutputLines = 2000

// readFileRange formats content with cat -n style line numbers, returning
// lines [offset, offset+limit) from the full content.
// offset is 1-based; 0 or 1 means start from the beginning.
// limit <= 0 means read to end (capped at maxOutputLines).
func readFileRange(content string, offset, limit int, totalLines int) string {
	lines := strings.Split(content, "\n")

	// Handle trailing newline: strings.Split on "abc\n" gives ["abc", ""]
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	totalLines = len(lines)

	// Convert 1-based offset to 0-based index
	startIdx := offset - 1
	if startIdx < 0 {
		startIdx = 0
	}
	if startIdx >= totalLines {
		return fmt.Sprintf("[File has %d lines. Offset %d is beyond end.]", totalLines, offset)
	}

	// Determine end index
	endIdx := totalLines
	if limit > 0 {
		endIdx = startIdx + limit
		if endIdx > totalLines {
			endIdx = totalLines
		}
	} else if startIdx == 0 {
		// No limit specified and starting from beginning: cap at maxOutputLines
		if endIdx > maxOutputLines {
			endIdx = maxOutputLines
		}
	}

	var buf strings.Builder
	for i := startIdx; i < endIdx; i++ {
		fmt.Fprintf(&buf, "%6d\t%s\n", i+1, lines[i])
	}

	// Truncation notice
	if endIdx < totalLines {
		fmt.Fprintf(&buf, "[File truncated: showing lines %d-%d of %d. Use read_file with offset/limit for more.]\n",
			startIdx+1, endIdx, totalLines)
	}

	return buf.String()
}
