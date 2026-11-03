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

func readFileRangeWithOptions(content string, offset, limit int, opts readFileRangeOptions) string {
	lines := strings.Split(content, "\n")

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

	startIdx := offset - 1
	if startIdx < 0 {
		startIdx = 0
	}
	if startIdx >= totalLines {
		return fmt.Sprintf("[File has %d lines. Offset %d is beyond end.]", totalLines, offset)
	}

	endIdx := totalLines
	if limit > 0 {
		endIdx = startIdx + limit
		if endIdx > totalLines {
			endIdx = totalLines
		}
	} else {

		endIdx = startIdx + opts.defaultLimit
		if endIdx > totalLines {
			endIdx = totalLines
		}
	}

	var buf strings.Builder
	for i := startIdx; i < endIdx; i++ {
		fmt.Fprintf(&buf, "%6d\t%s\n", i+1, lines[i])
	}

	if endIdx < totalLines {
		fmt.Fprintf(&buf, "[File truncated: showing lines %d-%d of %d. %s]\n",
			startIdx+1, endIdx, totalLines, opts.moreHint)
	}

	return buf.String()
}

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

	startIdx := offset - 1
	if startIdx < 0 {
		startIdx = 0
	}

	effectiveLimit := limit
	if effectiveLimit <= 0 {
		effectiveLimit = opts.defaultLimit
	}

	scanner := bufio.NewScanner(f)

	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var buf strings.Builder
	lineNum := 0
	readCount := 0
	hitLimit := false
	sawConflict := false
	conflictOpen := false
	for scanner.Scan() {
		if lineNum < startIdx {
			lineNum++
			continue
		}
		if readCount >= effectiveLimit {
			hitLimit = true

			break
		}
		fmt.Fprintf(&buf, "%6d\t%s\n", lineNum+1, scanner.Text())

		if raw := scanner.Text(); strings.HasPrefix(raw, "<<<<<<<") {
			sawConflict = true
			conflictOpen = true
		} else if conflictOpen && raw == "=======" {

		} else if conflictOpen && strings.HasPrefix(raw, ">>>>>>>") {
			conflictOpen = false
		}
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

		fmt.Fprintf(&buf, "[Showing lines %d-%d. More lines exist below - %s]\n",
			startIdx+1, startIdx+readCount, opts.moreHint)
	}

	if sawConflict {
		buf.WriteString("\n[WARNING] File contains merge conflict markers in this range. Resolve conflicts before editing.\n")
	}

	return buf.String(), nil
}

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
