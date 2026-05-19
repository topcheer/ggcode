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
		"Each edit follows the same rules as edit_file: the best practice is to paste the numbered lines directly from read_file into each old_text, because the tool uses those line numbers as anchors. Without line-number anchors, every old_text must match the file byte-for-byte (indentation, line endings) and must be UNIQUE in the file. " +
		"Edits must not overlap. All edits are applied atomically — if any single edit fails, the file is left unchanged. " +
		"All old_text matches are resolved against the ORIGINAL file, not after earlier edits in the same call. Always read_file first. " +
		"Use this instead of repeated edit_file calls when changing multiple sites in one file (e.g. renaming a symbol)."
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
		mr := resolveOldText(content, edit.OldText)
		if mr.canonical == "" {
			hint := diagnoseMatchFailure(content, edit.OldText)
			msg := fmt.Sprintf("edits[%d]: old_text not found in file", i)
			if hint != "" {
				msg += ". " + hint
			}
			return Result{IsError: true, Content: msg}, nil
		}
		oldText := mr.canonical
		count := strings.Count(content, oldText)
		if count > 1 && !mr.anchored {
			lines := findMatchLineNumbers(content, oldText)
			msg := fmt.Sprintf(
				"edits[%d]: old_text found %d times — must be unique. Add 1-3 lines of surrounding context to disambiguate, or copy the exact numbered lines from read_file so this edit is line-number anchored.",
				i, count,
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
		idx := mr.start
		if !mr.anchored {
			idx = strings.Index(content, oldText)
		}
		newText := edit.NewText
		if mr.transform != "" {
			newText = adjustNewText(content, edit.NewText, mr)
		}
		positions = append(positions, editPos{
			start: idx,
			end:   idx + len(oldText),
			idx:   i,
			old:   oldText,
			new_:  newText,
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
