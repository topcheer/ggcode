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
	return "Edit a file by replacing an exact text match with new text. The old_text must uniquely match in the file."
}

func (t EditFile) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"file_path": {
			"type": "string",
			"description": "Path to the file to edit"
		},
		"old_text": {
			"type": "string",
			"description": "Exact text to find and replace"
		},
		"new_text": {
			"type": "string",
			"description": "Replacement text"
		},
		"replace_all": {
			"type": "boolean",
			"description": "Replace all occurrences of old_text (default false)"
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Searching for TODO patterns', '检查构建配置'). You MUST always provide this field."
		}
	},
	"required": [
		"file_path",
		"old_text",
		"new_text",
		"description"
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

	// Try exact match first.
	oldText := args.OldText
	if !strings.Contains(content, oldText) {
		// Exact match failed — try whitespace normalization.
		// This handles the common case where the LLM uses spaces for indentation
		// but the file uses tabs (or vice versa), so it succeeds on the first
		// attempt without wasting an LLM round-trip.
		normalized := normalizeIndentation(content, oldText)
		if normalized != oldText && strings.Contains(content, normalized) {
			oldText = normalized
		} else {
			hint := diagnoseMatchFailure(content, args.OldText)
			msg := "old_text not found in file"
			if hint != "" {
				msg += ". " + hint
			}
			return Result{IsError: true, Content: msg}, nil
		}
	}

	count := strings.Count(content, oldText)
	if !args.ReplaceAll && count > 1 {
		return Result{IsError: true, Content: fmt.Sprintf("old_text found %d times in file — must be unique (use replace_all to replace all occurrences)", count)}, nil
	}

	// When we used a normalized match, also normalize new_text indentation
	// so the replacement preserves the file's indentation style.
	newText := args.NewText
	if oldText != args.OldText {
		newText = normalizeIndentation(content, args.NewText)
	}

	var newContent string
	if args.ReplaceAll {
		newContent = strings.ReplaceAll(content, oldText, newText)
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
func diagnoseMatchFailure(content, oldText string) string {
	oldHasTabs := strings.Contains(oldText, "\t")
	oldHasLeadingSpaces := false
	for _, line := range strings.Split(oldText, "\n") {
		if len(line) > 0 && line[0] == ' ' {
			oldHasLeadingSpaces = true
			break
		}
	}

	fileHasTabs := false
	fileIndentSpaces := 0
	lines := strings.Split(content, "\n")
	limit := len(lines)
	if limit > 100 {
		limit = 100
	}
	for _, line := range lines[:limit] {
		if len(line) > 0 && line[0] == '\t' {
			fileHasTabs = true
		}
		if len(line) > 0 && line[0] == ' ' {
			fileIndentSpaces++
		}
	}

	var hints []string

	if fileHasTabs && !oldHasTabs && oldHasLeadingSpaces {
		hints = append(hints, "file uses tab indentation — use \\t in old_text")
		firstOldLine := strings.Split(oldText, "\n")[0]
		trimmedOld := strings.TrimLeft(firstOldLine, " \t")
		if trimmedOld != "" {
			for _, line := range lines {
				trimmed := strings.TrimLeft(line, " \t")
				if trimmed == trimmedOld {
					visual := visualizeWhitespace(line)
					hints = append(hints, fmt.Sprintf("expected line: %s", visual))
					break
				}
			}
		}
	} else if !fileHasTabs && oldHasTabs && fileIndentSpaces > 0 {
		hints = append(hints, "file uses space indentation — remove \\t from old_text")
	}

	// Check for CRLF vs LF
	if strings.Contains(content, "\r\n") && !strings.Contains(oldText, "\r\n") {
		hints = append(hints, "file uses CRLF line endings — re-read the file to get exact content")
	}

	// Check if old_text is close to something in the file (first line matches partially)
	if len(hints) == 0 {
		firstOldLine := strings.Split(oldText, "\n")[0]
		firstOldLine = strings.TrimSpace(firstOldLine)
		if firstOldLine != "" {
			for _, line := range lines {
				trimmed := strings.TrimSpace(line)
				if trimmed == firstOldLine {
					visual := visualizeWhitespace(line)
					hints = append(hints, fmt.Sprintf(
						"first line matches but whitespace differs. Expected line: %s — re-read the file with read_file and copy exact content",
						visual,
					))
					break
				}
			}
		}
	}

	if len(hints) == 0 {
		hints = append(hints, "re-read the file with read_file and use exact content for old_text")
	}

	return strings.Join(hints, "; ")
}

// visualizeWhitespace replaces tab characters with the literal string "<TAB>"
// so the LLM can see the exact whitespace pattern in diagnostic messages.
func visualizeWhitespace(line string) string {
	return strings.ReplaceAll(line, "\t", "<TAB>")
}
