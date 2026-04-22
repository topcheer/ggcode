package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// MultiEditFile applies multiple find-and-replace edits to a single file in one call.
type MultiEditFile struct {
	SandboxCheck AllowedPathChecker
}

func (t MultiEditFile) Name() string { return "multi_edit_file" }

func (t MultiEditFile) Description() string {
	return "Apply multiple find-and-replace edits to a single file in one call. " +
		"Each old_text must uniquely match in the file. All edits are applied atomically."
}

func (t MultiEditFile) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"file_path": {
				"type": "string",
				"description": "Path to the file to edit"
			},
			"edits": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"old_text": {"type": "string", "description": "Exact text to find"},
						"new_text": {"type": "string", "description": "Replacement text"}
					},
					"required": ["old_text", "new_text"]
				},
				"description": "Array of edit operations to apply"
			}
		},
		"required": ["file_path", "edits"]
	}`)
}

func (t MultiEditFile) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		FilePath string `json:"file_path"`
		Edits    []struct {
			OldText string `json:"old_text"`
			NewText string `json:"new_text"`
		} `json:"edits"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if len(args.Edits) == 0 {
		return Result{IsError: true, Content: "edits array must not be empty"}, nil
	}

	if t.SandboxCheck != nil && !t.SandboxCheck(args.FilePath) {
		return Result{IsError: true, Content: "Error: path not allowed by sandbox policy"}, nil
	}

	data, err := os.ReadFile(args.FilePath)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error reading file: %v", err)}, nil
	}

	content := string(data)

	// Find all match positions first, validate uniqueness.
	type editPos struct {
		start int
		end   int
		idx   int
		old   string
		new_  string
	}
	var positions []editPos

	for i, edit := range args.Edits {
		if edit.OldText == "" {
			return Result{IsError: true, Content: fmt.Sprintf("edits[%d]: old_text must not be empty", i)}, nil
		}
		count := strings.Count(content, edit.OldText)
		if count == 0 {
			return Result{IsError: true, Content: fmt.Sprintf("edits[%d]: old_text not found in file", i)}, nil
		}
		if count > 1 {
			return Result{IsError: true, Content: fmt.Sprintf("edits[%d]: old_text found %d times — must be unique", i, count)}, nil
		}
		idx := strings.Index(content, edit.OldText)
		positions = append(positions, editPos{
			start: idx,
			end:   idx + len(edit.OldText),
			idx:   i,
			old:   edit.OldText,
			new_:  edit.NewText,
		})
	}

	// Check for overlapping edits.
	sort.Slice(positions, func(i, j int) bool { return positions[i].start < positions[j].start })
	for i := 1; i < len(positions); i++ {
		if positions[i].start < positions[i-1].end {
			return Result{IsError: true, Content: fmt.Sprintf(
				"edits[%d] and edits[%d]: overlapping matches — each old_text must not overlap",
				positions[i-1].idx, positions[i].idx)}, nil
		}
	}

	// Apply edits from back to front to preserve earlier positions.
	for i := len(positions) - 1; i >= 0; i-- {
		p := positions[i]
		content = content[:p.start] + p.new_ + content[p.end:]
	}

	if err := atomicWriteFile(args.FilePath, []byte(content), 0644); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error writing file: %v", err)}, nil
	}

	return Result{Content: fmt.Sprintf("Applied %d edits to %s", len(args.Edits), args.FilePath)}, nil
}
