package watchmode

import (
	"fmt"
	"strings"
)

// snippetRadius is how many lines of context surround the annotation line in
// the generated prompt.
const snippetRadius = 4

// BuildPrompt renders the pipe-mode prompt for a marker. snippet is the full
// file content as lines; only a window around the annotation is embedded.
func BuildPrompt(m Marker, snippet []string) string {
	var b strings.Builder
	b.WriteString("The user annotated a file with a @ggcode watch-mode marker while editing in their editor. Handle the annotation.\n\n")
	fmt.Fprintf(&b, "File: %s:%d\n", m.Path, m.Line)
	if m.Mode == ModeAsk {
		b.WriteString("Mode: ask — answer or explain ONLY. Do NOT modify any files.\n")
	} else {
		b.WriteString("Mode: act — perform the requested work; you may edit files and run commands.\n")
	}
	fmt.Fprintf(&b, "\nAnnotation:\n    %s\n", m.Instruction)
	start, window := SnippetAround(snippet, m.Line, snippetRadius)
	if len(window) > 0 {
		b.WriteString("\nSurrounding code:\n")
		for i, line := range window {
			fmt.Fprintf(&b, "%6d | %s\n", start+i, line)
		}
	}
	b.WriteString("\nCarry out the annotation now and give a concise final summary.\n")
	return b.String()
}

// SnippetAround returns a window of lines centered on lineNo (1-based) with
// the given radius, plus the 1-based number of the first returned line.
func SnippetAround(lines []string, lineNo, radius int) (int, []string) {
	if lineNo < 1 {
		lineNo = 1
	}
	start := lineNo - 1 - radius
	end := lineNo - 1 + radius + 1
	if end > len(lines) {
		end = len(lines)
	}
	if start >= end {
		// Line beyond EOF: fall back to the tail of the file.
		start = end - (2*radius + 1)
		if start < 0 {
			start = 0
		}
	}
	if start < 0 {
		start = 0
	}
	return start + 1, lines[start:end]
}
