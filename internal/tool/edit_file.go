package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// EditFile implements the edit_file tool for find-and-replace editing.
type EditFile struct {
	SandboxCheck AllowedPathChecker
}

func (t EditFile) Name() string { return "edit_file" }

func (t EditFile) Description() string {
	return "Edit a file by replacing one occurrence of old_text with new_text. " +
		"Rules for high success rate: " +
		"(1) ALWAYS read_file first to get the exact current content. " +
		"(2) Best practice: copy the relevant numbered lines directly from read_file into old_text. The tool understands read_file prefixes like \"   42\\t\" and uses them as anchors, which is especially helpful for single-line edits and duplicate text. " +
		"(3) If you do not use line-number anchors, old_text must still match the file byte-for-byte INCLUDING indentation (tabs vs spaces) and line endings. " +
		"(4) Without line-number anchors, old_text must be UNIQUE in the file; otherwise include 1-3 lines of surrounding context or set replace_all=true. " +
		"(5) On failure, the error message lists hints (indent style, near-matches with whitespace visualised, matching line numbers) — read them and adjust before retrying. " +
		"For multiple edits to the same file in one round-trip, prefer multi_edit_file."
}

func (t EditFile) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"file_path": {
			"type": "string",
			"description": "Path to the file to edit."
		},
		"old_text": {
			"type": "string",
			"description": "Text to find. Recommended: paste the numbered lines directly from read_file; the tool understands line-number prefixes and uses them as anchors. Without line numbers, old_text must match the file byte-for-byte (indentation, line endings) and be unique in the file."
		},
		"new_text": {
			"type": "string",
			"description": "Replacement text. If you copied numbered lines from read_file, you may keep or remove those prefixes here; they are stripped automatically."
		},
		"replace_all": {
			"type": "boolean",
			"description": "If true, replace every occurrence of old_text. Default false (the call fails if old_text is not unique)."
		},
		"description": {
			"type": "string",
			"description": "Optional. Brief activity label shown in the UI in the user's language (e.g. 'Updating retry policy', '更新缩进')."
		}
	},
	"required": [
		"file_path",
		"old_text",
		"new_text"
	]
}`)
}

func (t EditFile) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		FilePath   string `json:"file_path"`
		OldText    string `json:"old_text"`
		NewText    string `json:"new_text"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if t.SandboxCheck != nil && !t.SandboxCheck(args.FilePath) {
		return Result{IsError: true, Content: "Error: path not allowed by sandbox policy"}, nil
	}

	data, err := os.ReadFile(args.FilePath)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error reading file: %v", err)}, nil
	}

	content := string(data)

	mr := resolveOldText(content, args.OldText)
	if mr.canonical == "" {
		hint := diagnoseMatchFailure(content, args.OldText)
		msg := "old_text not found in file"
		if hint != "" {
			msg += ". " + hint
		}
		return Result{IsError: true, Content: msg}, nil
	}
	oldText := mr.canonical

	count := strings.Count(content, oldText)
	if !args.ReplaceAll && count > 1 && !mr.anchored {
		lines := findMatchLineNumbers(content, oldText)
		msg := fmt.Sprintf(
			"old_text found %d times in file — must be unique. Add 1-3 lines of surrounding context to disambiguate, copy the exact numbered lines from read_file to anchor the intended occurrence, or set replace_all=true to replace every occurrence.",
			count,
		)
		if len(lines) > 0 {
			more := ""
			if count > len(lines) {
				more = fmt.Sprintf(" (showing first %d)", len(lines))
			}
			msg += fmt.Sprintf(" Matches start at line(s): %s%s.", formatMatchLines(lines), more)
		}
		return Result{IsError: true, Content: msg}, nil
	}

	// Apply the same transform to new_text so the replacement matches the
	// file's conventions (indentation, line endings, line-number stripping).
	newText := args.NewText
	if mr.transform != "" {
		newText = adjustNewText(content, args.NewText, mr)
	}

	var newContent string
	if args.ReplaceAll {
		newContent = strings.ReplaceAll(content, oldText, newText)
	} else if mr.anchored {
		newContent = content[:mr.start] + newText + content[mr.start+len(oldText):]
	} else {
		newContent = strings.Replace(content, oldText, newText, 1)
	}

	if err := atomicWriteFile(args.FilePath, []byte(newContent), 0644); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error writing file: %v", err)}, nil
	}

	// Build a summary
	oldLines := strings.Count(args.OldText, "\n") + 1
	newLines := strings.Count(args.NewText, "\n") + 1
	if args.ReplaceAll {
		return Result{Content: fmt.Sprintf("Replaced %d occurrence(s) in %s: %d lines -> %d lines", count, args.FilePath, oldLines, newLines)}, nil
	}
	return Result{Content: fmt.Sprintf("Replaced 1 occurrence in %s: %d lines -> %d lines", args.FilePath, oldLines, newLines)}, nil
}

// normalizeIndentation converts the indentation of text to match the file's style.
// If the file uses tabs and text uses spaces, leading spaces are converted to tabs.
// If the file uses spaces and text uses tabs, leading tabs are converted to spaces.
// The width parameter is inferred from the file content.
func normalizeIndentation(fileContent, text string) string {
	fileUsesTabs := false
	fileTabWidth := 0
	{
		tabLines, spaceLines := 0, 0
		spaceWidths := map[int]int{}
		lines := strings.Split(fileContent, "\n")
		limit := len(lines)
		if limit > 200 {
			limit = 200
		}
		for _, line := range lines[:limit] {
			if len(line) == 0 {
				continue
			}
			if line[0] == '\t' {
				tabLines++
			} else if line[0] == ' ' {
				spaceLines++
				n := 0
				for n < len(line) && line[n] == ' ' {
					n++
				}
				if n >= 2 {
					spaceWidths[n]++
				}
			}
		}
		fileUsesTabs = tabLines > spaceLines
		if len(spaceWidths) > 0 {
			allW := make([]int, 0, len(spaceWidths))
			for w := range spaceWidths {
				allW = append(allW, w)
			}
			g := allW[0]
			for _, w := range allW[1:] {
				g = gcd(g, w)
			}
			if g < 2 {
				g = 2
			}
			fileTabWidth = g
		}
		if fileTabWidth == 0 {
			fileTabWidth = 4
		}
	}

	// Check what the text uses
	textHasTabs := false
	textHasLeadingSpaces := false
	for _, line := range strings.Split(text, "\n") {
		if len(line) > 0 && line[0] == '\t' {
			textHasTabs = true
		}
		if len(line) > 0 && line[0] == ' ' {
			textHasLeadingSpaces = true
		}
	}

	// File uses tabs, text uses spaces → convert leading spaces to tabs
	if fileUsesTabs && !textHasTabs && textHasLeadingSpaces {
		return convertSpacesToTabs(text, fileTabWidth)
	}

	// File uses spaces, text uses tabs → convert leading tabs to spaces
	if !fileUsesTabs && textHasTabs {
		return convertTabsToSpaces(text, fileTabWidth)
	}

	return text
}

// convertSpacesToTabs converts leading spaces to tabs based on tabWidth.
// For example, with tabWidth=4:
//
//	"    foo" → "\tfoo"
//	"        bar" → "\t\tbar"
//	"  baz" → "\tbaz"  (non-multiple: consume one tab for any leading spaces)
func convertSpacesToTabs(text string, tabWidth int) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if len(line) == 0 {
			continue
		}
		// Count leading spaces
		spaces := 0
		for spaces < len(line) && line[spaces] == ' ' {
			spaces++
		}
		if spaces == 0 {
			continue
		}
		tabs := spaces / tabWidth
		remainder := spaces % tabWidth
		// Use at least 1 tab if there are any leading spaces
		if tabs == 0 && spaces > 0 {
			tabs = 1
			remainder = 0
		}
		lines[i] = strings.Repeat("\t", tabs) + strings.Repeat(" ", remainder) + line[spaces:]
	}
	return strings.Join(lines, "\n")
}

// convertTabsToSpaces converts leading tabs to spaces based on tabWidth.
func convertTabsToSpaces(text string, tabWidth int) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if len(line) == 0 {
			continue
		}
		tabs := 0
		for tabs < len(line) && line[tabs] == '\t' {
			tabs++
		}
		if tabs == 0 {
			continue
		}
		rest := line[tabs:]
		lines[i] = strings.Repeat(" ", tabs*tabWidth) + rest
	}
	return strings.Join(lines, "\n")
}

// diagnoseMatchFailure provides actionable hints when old_text is not found.
// It checks for common causes: tab vs space, trailing whitespace, line ending differences.
// When possible, it also finds the nearest matching lines in the file so the
// agent can see exactly what changed without a separate read_file call.
func diagnoseMatchFailure(content, oldText string) string {
	oldHasTabs := strings.Contains(oldText, "\t")
	oldHasLeadingSpaces := false
	for _, line := range strings.Split(oldText, "\n") {
		if len(line) > 0 && line[0] == ' ' {
			oldHasLeadingSpaces = true
			break
		}
	}

	allLines := strings.Split(content, "\n")

	// Detect indentation style from a sample of lines
	fileHasTabs := false
	fileIndentSpaces := 0
	sampleLimit := len(allLines)
	if sampleLimit > 100 {
		sampleLimit = 100
	}
	for _, line := range allLines[:sampleLimit] {
		if len(line) > 0 && line[0] == '\t' {
			fileHasTabs = true
		}
		if len(line) > 0 && line[0] == ' ' {
			fileIndentSpaces++
		}
	}

	var hints []string

	// Check for CRLF vs LF
	if strings.Contains(content, "\r\n") && !strings.Contains(oldText, "\r\n") {
		hints = append(hints, "file uses CRLF line endings — re-read the file to get exact content")
	}

	// Check for tab/space mismatch
	if fileHasTabs && !oldHasTabs && oldHasLeadingSpaces {
		hints = append(hints, "file uses tab indentation — use \\t in old_text")
	} else if !fileHasTabs && oldHasTabs && fileIndentSpaces > 0 {
		hints = append(hints, "file uses space indentation — remove \\t from old_text")
	}

	// Try to find the nearest matching lines in the full file.
	// This is the most useful diagnostic: it shows the agent exactly what the
	// file looks like near the intended edit location, with line numbers that
	// can be used as anchors for a corrected edit_file retry.
	if nearest := findNearestLines(allLines, oldText, 3); len(nearest) > 0 {
		var parts []string
		for _, nl := range nearest {
			parts = append(parts, fmt.Sprintf("  %d\t%s", nl.lineNum, visualizeWhitespace(nl.text)))
		}
		hints = append(hints, fmt.Sprintf("nearest matching lines in file:\n%s", strings.Join(parts, "\n")))
	}

	if len(hints) == 0 {
		hints = append(hints, "re-read the file with read_file and use exact content for old_text")
	}

	return strings.Join(hints, "; ")
}

// nearestLine holds a line's content and its 1-based line number.
type nearestLine struct {
	lineNum int
	text    string
}

// findNearestLines searches the entire file for lines most similar to the
// first line of oldText. It returns up to `maxResults` lines sorted by
// similarity (descending). Uses token-level Jaccard similarity which is
// fast and works well for code.
func findNearestLines(fileLines []string, oldText string, maxResults int) []nearestLine {
	oldFirst := strings.TrimSpace(strings.Split(oldText, "\n")[0])
	if oldFirst == "" || len(oldFirst) < 3 {
		return nil
	}

	oldTokens := tokenize(oldFirst)
	if len(oldTokens) == 0 {
		return nil
	}

	type scored struct {
		nearestLine
		score float64
	}
	var candidates []scored

	for i, line := range fileLines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		lineTokens := tokenize(trimmed)
		if len(lineTokens) == 0 {
			continue
		}
		score := jaccardSimilarity(oldTokens, lineTokens)
		if score > 0.3 { // minimum threshold for "similar enough"
			candidates = append(candidates, scored{
				nearestLine: nearestLine{lineNum: i + 1, text: line},
				score:       score,
			})
		}
	}

	// Sort by score descending, keep top N
	for i := 0; i < len(candidates); i++ {
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].score > candidates[i].score {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}
	if len(candidates) > maxResults {
		candidates = candidates[:maxResults]
	}

	result := make([]nearestLine, len(candidates))
	for i, c := range candidates {
		result[i] = c.nearestLine
	}
	return result
}

// tokenize splits a string into lowercase tokens for fuzzy comparison.
func tokenize(s string) map[string]struct{} {
	tokens := make(map[string]struct{})
	var current strings.Builder
	for _, c := range strings.ToLower(s) {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' {
			current.WriteRune(c)
		} else {
			if current.Len() > 0 {
				tokens[current.String()] = struct{}{}
				current.Reset()
			}
		}
	}
	if current.Len() > 0 {
		tokens[current.String()] = struct{}{}
	}
	return tokens
}

// jaccardSimilarity computes the Jaccard similarity between two token sets.
// J(A,B) = |A ∩ B| / |A ∪ B|
func jaccardSimilarity(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	intersection := 0
	for token := range a {
		if _, ok := b[token]; ok {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// visualizeWhitespace replaces tab characters with the literal string "<TAB>"
// so the LLM can see the exact whitespace pattern in diagnostic messages.
func visualizeWhitespace(line string) string {
	return strings.ReplaceAll(line, "\t", "<TAB>")
}
