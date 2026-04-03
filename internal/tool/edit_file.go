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
			}
		},
		"required": ["file_path", "old_text", "new_text"]
	}`)
}

func (t EditFile) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		FilePath string `json:"file_path"`
		OldText  string `json:"old_text"`
		NewText  string `json:"new_text"`
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
		return Result{IsError: true, Content: "old_text not found in file"}, nil
	}

	count := strings.Count(content, args.OldText)
	if count > 1 {
		return Result{IsError: true, Content: fmt.Sprintf("old_text found %d times in file — must be unique", count)}, nil
	}

	newContent := strings.Replace(content, args.OldText, args.NewText, 1)

	if err := os.WriteFile(args.FilePath, []byte(newContent), 0644); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error writing file: %v", err)}, nil
	}

	// Build a summary
	oldLines := strings.Count(args.OldText, "\n") + 1
	newLines := strings.Count(args.NewText, "\n") + 1
	return Result{Content: fmt.Sprintf("Replaced 1 occurrence in %s: %d lines -> %d lines", args.FilePath, oldLines, newLines)}, nil
}
