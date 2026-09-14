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
	} else {
		// No limit specified: cap at defaultLimit in BOTH branches (#855).
		// Previously the cap only applied when startIdx==0, so an offset read
		// without limit dumped the entire file remainder, contradicting the
		// doc ('capped at maxOutputLines') and the streaming variant.
		endIdx = startIdx + opts.defaultLimit
		if endIdx > totalLines {
			endIdx = totalLines
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
	sawConflict := false  // #2269: raw-line conflict marker seen in range
	conflictOpen := false // #2269 R100: region state (only <<<<<<< opens)
	for scanner.Scan() {
		if lineNum < startIdx {
			lineNum++
			continue
		}
		if readCount >= effectiveLimit {
			hitLimit = true
			// #1698 case 6: stop scanning once past the range - the old code
			// kept scanning a multi-GB file to EOF solely to compute an exact
			// total-line count for the "~N lines" hint, defeating the point
			// of streaming range reads.
			break
		}
		fmt.Fprintf(&buf, "%6d\t%s\n", lineNum+1, scanner.Text())
		// #2269 (R100 tightening): a conflict region STATE MACHINE,
		// mirroring DetectMergeConflicts semantics - only "<<<<<<<" opens
		// a region, "=======" must be exactly 7 equals AND inside an open
		// region, ">>>>>>>" closes it. The first cut fired on any of the
		// three markers independently, so a lone setext underline or a
		// quoted ">>>>>>>" in a >10MB markdown file raised a false
		// WARNING (review: sa-211).
		if raw := scanner.Text(); strings.HasPrefix(raw, "<<<<<<<") {
			sawConflict = true
			conflictOpen = true
		} else if conflictOpen && raw == "=======" {
			// in-region divider: nothing extra to set
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
		// #2306: totalLines == startIdx+readCount here (the #1698 case-6
		// early break stops the scan at the limit), so printing "of ~N"
		// showed the COUNT ALREADY DISPLAYED as the approximate total -
		// "lines 1-2000 of ~2000" - and the agent concluded the file was
		// fully read and stopped paginating. When truncated, the total is
		// unknowable without an EOF scan; say "more below" without a
		// number instead of fabricating one.
		fmt.Fprintf(&buf, "[Showing lines %d-%d. More lines exist below - %s]\n",
			startIdx+1, startIdx+readCount, opts.moreHint)
	}

	// #2269: report the conflict warning the caller-side guard could
	// never produce (its input was this line-numbered text). Mirrors
	// CheckContentForConflicts' banner format.
	if sawConflict {
		buf.WriteString("\n[WARNING] File contains merge conflict markers in this range. Resolve conflicts before editing.\n")
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
