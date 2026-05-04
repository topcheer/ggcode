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
			}
		},
		"required": ["file_path", "old_text", "new_text"]
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

	if !strings.Contains(content, args.OldText) {
		hint := diagnoseMatchFailure(content, args.OldText)
		msg := "old_text not found in file"
		if hint != "" {
			msg += ". " + hint
		}
		return Result{IsError: true, Content: msg}, nil
	}

	count := strings.Count(content, args.OldText)
	if !args.ReplaceAll && count > 1 {
		return Result{IsError: true, Content: fmt.Sprintf("old_text found %d times in file — must be unique (use replace_all to replace all occurrences)", count)}, nil
	}

	var newContent string
	if args.ReplaceAll {
		newContent = strings.ReplaceAll(content, args.OldText, args.NewText)
	} else {
		newContent = strings.Replace(content, args.OldText, args.NewText, 1)
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

// diagnoseMatchFailure provides actionable hints when old_text is not found.
// It checks for common causes: tab vs space, trailing whitespace, line ending differences.
func diagnoseMatchFailure(content, oldText string) string {
	// Quick check: does old_text use spaces where file uses tabs (or vice versa)?
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
					hints = append(hints, "first line matches but whitespace differs — re-read the file with read_file and copy exact content")
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
