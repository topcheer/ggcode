package tool

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

const maxOutputLines = 2000

type readFileRangeOptions struct {
	defaultLimit int
	moreHint     string
}

// readFileRange formats content with cat -n style line numbers, returning
// lines [offset, offset+limit) from the full content.
// offset is 1-based; 0 or 1 means start from the beginning.
// limit <= 0 means read to end (capped at maxOutputLines).
func readFileRange(content string, offset, limit int, totalLines int) string {
	return readFileRangeWithOptions(content, offset, limit, readFileRangeOptions{
		defaultLimit: maxOutputLines,
		moreHint:     "Use read_file with offset/limit for more.",
	})
}

func readFileRangeWithOptions(content string, offset, limit int, opts readFileRangeOptions) string {
	lines := strings.Split(content, "\n")

	// Handle trailing newline: strings.Split on "abc\n" gives ["abc", ""]
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	totalLines := len(lines)
	if opts.defaultLimit <= 0 {
		opts.defaultLimit = maxOutputLines
	}
	if opts.moreHint == "" {
		opts.moreHint = "Use read_file with offset/limit for more."
	}

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
		// No limit specified and starting from beginning: cap at defaultLimit.
		if endIdx > opts.defaultLimit {
			endIdx = opts.defaultLimit
		}
	}

	var buf strings.Builder
	for i := startIdx; i < endIdx; i++ {
		fmt.Fprintf(&buf, "%6d\t%s\n", i+1, lines[i])
	}

	// Truncation notice
	if endIdx < totalLines {
		fmt.Fprintf(&buf, "[File truncated: showing lines %d-%d of %d. %s]\n",
			startIdx+1, endIdx, totalLines, opts.moreHint)
	}

	return buf.String()
}

// readFileRangeStreaming reads a specific line range from a file using a
// streaming scanner, without loading the entire file into memory. This is
// used for large files (>10MB) where the agent specifies offset/limit.
// The output format matches readFileRangeWithOptions (cat -n style).
func readFileRangeStreaming(path string, offset, limit int, opts readFileRangeOptions) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("error opening file: %w", err)
	}
	defer f.Close()

	if opts.defaultLimit <= 0 {
		opts.defaultLimit = maxOutputLines
	}
	if opts.moreHint == "" {
		opts.moreHint = "Use read_file with offset/limit for more."
	}

	// Convert 1-based offset to 0-based
	startIdx := offset - 1
	if startIdx < 0 {
		startIdx = 0
	}

	// Determine how many lines to read
	effectiveLimit := limit
	if effectiveLimit <= 0 {
		effectiveLimit = opts.defaultLimit
	}

	scanner := bufio.NewScanner(f)
	// Allow lines up to 1MB (for minified files with very long lines)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var buf strings.Builder
	lineNum := 0 // 0-based
	readCount := 0
	hitLimit := false
	for scanner.Scan() {
		if lineNum < startIdx {
			lineNum++
			continue
		}
		if readCount >= effectiveLimit {
			hitLimit = true
			// Don't break — keep scanning to count total lines
			lineNum++
			continue
		}
		fmt.Fprintf(&buf, "%6d\t%s\n", lineNum+1, scanner.Text())
		readCount++
		lineNum++
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("error reading file: %w", err)
	}

	totalLines := lineNum

	if buf.Len() == 0 {
		if startIdx > 0 && totalLines > 0 {
			return fmt.Sprintf("[File has ~%d lines. Offset %d is beyond end.]", totalLines, offset), nil
		}
		return "[Empty file or no lines in range.]", nil
	}

	if hitLimit {
		fmt.Fprintf(&buf, "[Showing lines %d-%d of ~%d. %s]\n",
			startIdx+1, startIdx+readCount, totalLines, opts.moreHint)
	}

	return buf.String(), nil
}

// countFileLines does a fast line count of a file using a streaming scanner.
// Used to provide line count hints for large files without loading them into memory.
func countFileLines(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	count := 0
	for scanner.Scan() {
		count++
	}
	return count
}
