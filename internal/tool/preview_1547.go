package tool

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

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

		return nil, fmt.Errorf("%s", msg)
	}
	return []PlannedFileEdit{{Path: resolved, OldContent: original, NewContent: content, Existed: true}}, nil
}

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
		old, rerr := os.ReadFile(resolved)
		existed := rerr == nil || !os.IsNotExist(rerr)
		plans = append(plans, PlannedFileEdit{Path: resolved, OldContent: string(old), NewContent: f.Content, Existed: existed})
	}
	return plans, nil
}

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

	if len(args.Pattern) > maxBatchReplacePatternLen {
		return nil, fmt.Errorf("pattern too long: %d bytes, max %d", len(args.Pattern), maxBatchReplacePatternLen)
	}
	if len(args.Replacement) > maxBatchReplaceReplaceLen {
		return nil, fmt.Errorf("replacement too long: %d bytes, max %d", len(args.Replacement), maxBatchReplaceReplaceLen)
	}
	if len(args.Files) > maxBatchReplaceFiles {
		return nil, fmt.Errorf("too many files: %d, max %d", len(args.Files), maxBatchReplaceFiles)
	}
	var re *regexp.Regexp
	if args.IsRegex {
		compiled, err := regexp.Compile(args.Pattern)
		if err != nil {

			return nil, err
		}
		re = compiled

		if bad := invalidGroupRefs(re, args.Replacement); bad != "" {
			return nil, fmt.Errorf("replacement references capture group %s but the pattern has only %d group(s); use $$ for a literal $", bad, re.NumSubexp())
		}
	}
	plans := make([]PlannedFileEdit, 0, len(args.Files))
	for _, f := range args.Files {
		resolved, err := resolveToolPath(f, t.WorkingDir)
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(resolved)
		if err != nil {
			continue
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
			plans = append(plans, PlannedFileEdit{Path: resolved, OldContent: content, NewContent: newContent, Existed: true})
		}
	}
	return plans, nil
}
