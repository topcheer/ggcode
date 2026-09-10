package tool

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// PreviewChanges implementations for the fan-out write tools that were
// routed through the bare safeExecute path (#1547 case A): a wrong batch
// edit could punch through N files with no pre-write gate - the most
// dangerous fan-out tools were exactly the unprotected ones.
//
// notebook_edit and lsp_rename stay exempt: the notebook preview value is
// nil (JSON, not Go - the syntax gate is .go-scoped) and lsp_rename's
// resulting content is produced by the language server, unknowable
// pre-execution. file_ops move/delete cannot be dry-run (no content).

// PreviewChanges plans multi_edit_file's in-memory result using the exact
// planTextEdits matcher the executor uses.
func (t MultiEditFile) PreviewChanges(input json.RawMessage) ([]PlannedFileEdit, error) {
	var args struct {
		FilePath string `json:"file_path"`
		Edits    []struct {
			OldText string `json:"old_text"`
			NewText string `json:"new_text"`
		} `json:"edits"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, err
	}
	if args.FilePath == "" {
		return nil, fmt.Errorf("missing file_path")
	}
	resolved, err := resolveToolPath(args.FilePath, t.WorkingDir)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return nil, err
	}
	edits := make([]textEdit, len(args.Edits))
	for i, e := range args.Edits {
		edits[i] = textEdit{OldText: e.OldText, NewText: e.NewText}
	}
	original := string(data)
	content, _, msg := planTextEdits(original, edits)
	if msg != "" {
		// Anchor mismatch: let the executor surface its error verbatim.
		return nil, fmt.Errorf("%s", msg)
	}
	return []PlannedFileEdit{{Path: resolved, OldContent: original, NewContent: content}}, nil
}

// PreviewChanges plans multi_file_write (full content is given directly).
func (t MultiFileWrite) PreviewChanges(input json.RawMessage) ([]PlannedFileEdit, error) {
	var args struct {
		Files []struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		} `json:"files"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, err
	}
	plans := make([]PlannedFileEdit, 0, len(args.Files))
	for _, f := range args.Files {
		resolved, err := resolveToolPath(f.Path, t.WorkingDir)
		if err != nil {
			return nil, err
		}
		old, _ := os.ReadFile(resolved) // missing file = create, old ""
		plans = append(plans, PlannedFileEdit{Path: resolved, OldContent: string(old), NewContent: f.Content})
	}
	return plans, nil
}

// PreviewChanges plans batch_replace by simulating the executor's own
// regex/literal replacement per file.
func (t BatchReplace) PreviewChanges(input json.RawMessage) ([]PlannedFileEdit, error) {
	var args struct {
		Pattern     string   `json:"pattern"`
		Replacement string   `json:"replacement"`
		IsRegex     bool     `json:"is_regex"`
		Files       []string `json:"files"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, err
	}
	if args.Pattern == "" || len(args.Files) == 0 {
		return nil, fmt.Errorf("missing pattern or files")
	}
	var re *regexp.Regexp
	if args.IsRegex {
		compiled, err := regexp.Compile(args.Pattern)
		if err != nil {
			// Invalid regex: the executor errors out anyway - let it speak.
			return nil, err
		}
		re = compiled
	}
	plans := make([]PlannedFileEdit, 0, len(args.Files))
	for _, f := range args.Files {
		resolved, err := resolveToolPath(f, t.WorkingDir)
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(resolved)
		if err != nil {
			continue // executor reports per-file errors; preview skips
		}
		content := string(data)
		var newContent string
		if re != nil {
			if re.MatchString(content) {
				newContent = re.ReplaceAllString(content, args.Replacement)
			} else {
				newContent = content
			}
		} else {
			if strings.Contains(content, args.Pattern) {
				newContent = strings.ReplaceAll(content, args.Pattern, args.Replacement)
			} else {
				newContent = content
			}
		}
		if newContent != content {
			plans = append(plans, PlannedFileEdit{Path: resolved, OldContent: content, NewContent: newContent})
		}
	}
	return plans, nil
}
