package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
)

// MultiEditFile applies multiple find-and-replace edits to a single file in one call.
type MultiEditFile struct {
	SandboxCheck AllowedPathChecker
}

func (t MultiEditFile) Name() string { return "multi_edit_file" }

func (t MultiEditFile) Description() string {
	return "Apply multiple find-and-replace edits to a single file in one call. " +
		"Same matching rules as edit_file (use numbered lines from read_file as anchors). " +
		"All edits resolve against the ORIGINAL file and must not overlap. Applied atomically — if any fails, the file is unchanged."
}

func (t MultiEditFile) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"file_path": {
			"type": "string",
			"description": "Path to the file to edit."
		},
		"edits": {
			"type": "array",
			"items": {
				"type": "object",
				"properties": {
					"old_text": {
						"type": "string",
						"description": "Text to find. Recommended: paste the numbered lines directly from read_file; the tool uses those prefixes as anchors. Without line numbers, old_text must match byte-for-byte (indentation, line endings) and be unique in the file."
					},
					"new_text": {
						"type": "string",
						"description": "Replacement text. If you copied numbered lines from read_file, you may keep or remove those prefixes here; they are stripped automatically."
					}
				},
				"required": [
					"old_text",
					"new_text"
				]
			},
			"description": "Edits to apply, in any order. Each old_text must be unique in the file and not overlap with other edits."
		},
		"description": {
			"type": "string",
			"description": "Optional. Brief activity label shown in the UI in the user's language."
		}
	},
	"required": [
		"file_path",
		"edits"
	]
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
	edits := make([]textEdit, len(args.Edits))
	for i, edit := range args.Edits {
		edits[i] = textEdit{OldText: edit.OldText, NewText: edit.NewText}
	}
	content, _, msg := planTextEdits(content, edits)
	if msg != "" {
		return Result{IsError: true, Content: msg}, nil
	}

	if err := atomicWriteFile(args.FilePath, []byte(content), 0644); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error writing file: %v", err)}, nil
	}

	return Result{Content: fmt.Sprintf("Applied %d edits to %s", len(args.Edits), args.FilePath)}, nil
}
