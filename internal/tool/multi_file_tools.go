package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

type MultiFileRead struct {
	SandboxCheck AllowedPathChecker
}

func (t MultiFileRead) Name() string { return "multi_file_read" }

func (t MultiFileRead) Description() string {
	return "Read multiple existing files in one call before a coordinated multi-file edit. " +
		"Recommended workflow: use multi_file_read first, then copy the numbered lines you want to change directly into multi_file_edit old_text. " +
		"Output uses deterministic file blocks like `=== FILE: /abs/path ===`, numbered `cat -n` style lines (`   42\\t<content>`), and `[end file]`, so weak models can copy/paste anchors instead of reconstructing text. " +
		"Keep batches small, paths absolute and unique, and use offset/limit to narrow large files."
}

func (t MultiFileRead) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"files": {
			"type": "array",
			"description": "Files to read, returned in the same order. Keep batches small, use absolute unique paths, and prefer offset/limit around the lines you plan to edit.",
			"items": {
				"type": "object",
				"properties": {
					"path": {
						"type": "string",
						"description": "Absolute path to the existing file to read."
					},
					"offset": {
						"type": "integer",
						"description": "1-based line number to start from. Omit or use 0 to read from the beginning."
					},
					"limit": {
						"type": "integer",
						"description": "Maximum number of lines to read for this file. Omit to use the multi_file_read default cap. Prefer narrow ranges around the target edit."
					}
				},
				"required": ["path"]
			}
		},
		"description": {
			"type": "string",
			"description": "Optional. Brief activity label shown in the UI in the user's language."
		}
	},
	"required": ["files"]
}`)
}

func (t MultiFileRead) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	_ = ctx
	var args struct {
		Files []struct {
			Path   string `json:"path"`
			Offset int    `json:"offset"`
			Limit  int    `json:"limit"`
		} `json:"files"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if len(args.Files) == 0 {
		return Result{IsError: true, Content: "files array must not be empty"}, nil
	}
	if len(args.Files) > maxMultiFileReadFiles {
		return Result{IsError: true, Content: fmt.Sprintf("too many files: got %d, max %d. Split the read into smaller batches.", len(args.Files), maxMultiFileReadFiles)}, nil
	}

	type readReq struct {
		Path   string
		Offset int
		Limit  int
	}
	reqs := make([]readReq, len(args.Files))
	seen := map[string]struct{}{}
	for i, f := range args.Files {
		path, err := cleanAbsolutePath(f.Path)
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("files[%d]: %v", i, err)}, nil
		}
		if _, ok := seen[path]; ok {
			return Result{IsError: true, Content: fmt.Sprintf("files[%d]: duplicate path %s", i, path)}, nil
		}
		seen[path] = struct{}{}
		if f.Offset < 0 {
			return Result{IsError: true, Content: fmt.Sprintf("files[%d]: offset must be >= 0", i)}, nil
		}
		if f.Limit < 0 {
			return Result{IsError: true, Content: fmt.Sprintf("files[%d]: limit must be >= 0", i)}, nil
		}
		if f.Limit > maxExplicitMultiFileReadLimit {
			return Result{IsError: true, Content: fmt.Sprintf("files[%d]: limit %d exceeds max %d. Narrow the range or split the batch.", i, f.Limit, maxExplicitMultiFileReadLimit)}, nil
		}
		reqs[i] = readReq{Path: path, Offset: f.Offset, Limit: f.Limit}
	}

	var body strings.Builder
	succeeded, failed, skipped := 0, 0, 0
	currentLines, currentBytes := 0, 0
	limitReached := false
	for i, req := range reqs {
		if limitReached {
			body.WriteString(formatMultiFileReadSkippedBlock(req.Path))
			skipped++
			continue
		}

		var block string
		if t.SandboxCheck != nil && !t.SandboxCheck(req.Path) {
			block = formatMultiFileReadErrorBlock(req.Path, "Error: path not allowed by sandbox policy")
		} else {
			text, err := readTextContentAtPath(req.Path, req.Offset, req.Limit, readFileRangeOptions{
				defaultLimit: defaultMultiFileReadLimit,
				moreHint:     "Use multi_file_read or read_file with a narrower range for more.",
			})
			if err != nil {
				block = formatMultiFileReadErrorBlock(req.Path, err.Error())
			} else {
				block = formatMultiFileReadFileBlock(req.Path, text)
			}
		}

		blockLines := strings.Count(block, "\n")
		blockBytes := len(block)
		if currentLines+blockLines > maxMultiFileReadTotalLines || currentBytes+blockBytes > maxMultiFileReadTotalBytes {
			limitReached = true
			for _, remaining := range reqs[i:] {
				body.WriteString(formatMultiFileReadSkippedBlock(remaining.Path))
				skipped++
			}
			break
		}
		body.WriteString(block)
		currentLines += blockLines
		currentBytes += blockBytes
		if strings.HasPrefix(block, "=== ERROR: ") {
			failed++
		} else {
			succeeded++
		}
	}

	summary := fmt.Sprintf("[multi_file_read summary] requested=%d succeeded=%d failed=%d skipped=%d", len(reqs), succeeded, failed, skipped)
	if skipped > 0 {
		summary += " [split batch or narrow ranges to read skipped files]"
	}
	content := summary
	if body.Len() > 0 {
		content += "\n\n" + strings.TrimSuffix(body.String(), "\n")
	}
	return Result{Content: content, IsError: false}, nil
}

type MultiFileEdit struct {
	SandboxCheck AllowedPathChecker
}

func (t MultiFileEdit) Name() string { return "multi_file_edit" }

func (t MultiFileEdit) Description() string {
	return "Apply coordinated edits across multiple existing files in one call. " +
		"Recommended workflow: 1) read target files with multi_file_read or read_file, 2) paste the numbered lines directly into old_text, 3) put the desired replacement in new_text. " +
		"Group all edits for the same file into one files[] entry. Within each file, all old_text matches are resolved against the ORIGINAL file content, not after earlier edits in the same request. " +
		"Default mode is atomic: if any file fails to plan, no files are written. Use mode=partial_success only when mixed write outcomes are acceptable."
}

func (t MultiFileEdit) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"mode": {
			"type": "string",
			"enum": ["atomic", "partial_success"],
			"description": "Optional. Defaults to atomic. atomic writes no files if any file fails to plan. partial_success writes successful files and reports failures separately."
		},
		"files": {
			"type": "array",
			"description": "Files to edit in one coordinated change. Each path must be absolute and unique within the request. Group all edits for the same file into one entry.",
			"items": {
				"type": "object",
				"properties": {
					"path": {
						"type": "string",
						"description": "Absolute path to the existing file to edit."
					},
					"edits": {
						"type": "array",
						"description": "Non-overlapping edits for this file. All old_text matches are resolved against the original file content, not after earlier edits in this same files[] entry.",
						"items": {
							"type": "object",
							"properties": {
								"old_text": {
									"type": "string",
									"description": "Text to find. Best practice: paste the numbered lines directly from multi_file_read/read_file, including the line-number prefixes. Without those anchors, old_text must still closely match the file content."
								},
								"new_text": {
									"type": "string",
									"description": "Replacement text. You may keep or remove copied line-number prefixes and multi_file_read/read_file wrapper lines; they are stripped automatically."
								}
							},
							"required": ["old_text", "new_text"]
						}
					}
				},
				"required": ["path", "edits"]
			}
		},
		"description": {
			"type": "string",
			"description": "Optional. Brief activity label shown in the UI in the user's language."
		}
	},
	"required": ["files"]
}`)
}

func (t MultiFileEdit) PreviewChanges(input json.RawMessage) ([]PlannedFileEdit, error) {
	mode, entries, errRes := t.parseInput(input)
	if errRes != nil {
		return nil, errors.New(errRes.Content)
	}
	plans, _, hasFailures := t.planEntries(entries)
	if mode == "atomic" && hasFailures {
		return nil, nil
	}
	return plans, nil
}

func (t MultiFileEdit) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	_ = ctx
	mode, entries, errRes := t.parseInput(input)
	if errRes != nil {
		return *errRes, nil
	}
	plans, results, hasFailures := t.planEntries(entries)
	byPath := make(map[string]int, len(results))
	for i, r := range results {
		byPath[r.Path] = i
	}

	out := MultiFileEditContent{
		Mode:         mode,
		PlannedFiles: len(plans),
		Results:      results,
	}

	if mode == "atomic" && hasFailures {
		for _, plan := range plans {
			idx := byPath[plan.Path]
			out.Results[idx].Status = "planned"
			out.Results[idx].AppliedEditCount = plan.AppliedEditCount
			out.SkippedPaths = append(out.SkippedPaths, plan.Path)
		}
		out.FailedFiles = countMultiFileEditStatus(out.Results, "error")
		out.SkippedFiles = len(out.SkippedPaths)
		out.FailedPaths = collectMultiFileEditPaths(out.Results, "error")
		out.Summary = fmt.Sprintf("Requested %d files: 0 written, %d failed, %d skipped by atomic mode", len(entries), out.FailedFiles, out.SkippedFiles)
		content, err := json.Marshal(out)
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("error marshaling result: %v", err)}, nil
		}
		return Result{IsError: true, Content: string(content)}, nil
	}

	if mode == "atomic" {
		if failedPath, writeErr := applyAtomicPlans(plans); writeErr != nil {
			for _, plan := range plans {
				idx := byPath[plan.Path]
				if plan.Path == failedPath {
					out.Results[idx].Status = "error"
					out.Results[idx].Error = writeErr.Error()
					out.FailedPaths = append(out.FailedPaths, plan.Path)
					continue
				}
				out.Results[idx].Status = "planned"
				out.Results[idx].AppliedEditCount = plan.AppliedEditCount
				out.SkippedPaths = append(out.SkippedPaths, plan.Path)
			}
			out.FailedFiles = len(out.FailedPaths)
			out.SkippedFiles = len(out.SkippedPaths)
			out.Summary = fmt.Sprintf("Requested %d files: 0 written, %d failed, %d skipped by atomic mode", len(entries), out.FailedFiles, out.SkippedFiles)
			content, err := json.Marshal(out)
			if err != nil {
				return Result{IsError: true, Content: fmt.Sprintf("error marshaling result: %v", err)}, nil
			}
			return Result{IsError: true, Content: string(content)}, nil
		}
		for _, plan := range plans {
			idx := byPath[plan.Path]
			out.Results[idx].Status = "success"
			out.Results[idx].AppliedEditCount = plan.AppliedEditCount
			out.WrittenPaths = append(out.WrittenPaths, plan.Path)
		}
		out.WrittenFiles = len(out.WrittenPaths)
		out.Applied = true
		out.Summary = fmt.Sprintf("Requested %d files: %d written, 0 failed, 0 skipped", len(entries), out.WrittenFiles)
		content, err := json.Marshal(out)
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("error marshaling result: %v", err)}, nil
		}
		return Result{IsError: false, Content: string(content)}, nil
	}

	planByPath := make(map[string]PlannedFileEdit, len(plans))
	for _, plan := range plans {
		planByPath[plan.Path] = plan
	}
	for _, plan := range plans {
		if err := atomicWriteFile(plan.Path, []byte(plan.NewContent), 0644); err != nil {
			idx := byPath[plan.Path]
			out.Results[idx].Status = "error"
			out.Results[idx].Error = fmt.Sprintf("error writing file: %v", err)
			out.FailedPaths = append(out.FailedPaths, plan.Path)
			continue
		}
		idx := byPath[plan.Path]
		out.Results[idx].Status = "success"
		out.Results[idx].AppliedEditCount = plan.AppliedEditCount
		out.WrittenPaths = append(out.WrittenPaths, plan.Path)
	}
	out.WrittenFiles = len(out.WrittenPaths)
	out.FailedFiles = countMultiFileEditStatus(out.Results, "error")
	for _, result := range out.Results {
		if result.Status == "error" && result.Path != "" && !containsStringValue(out.FailedPaths, result.Path) {
			out.FailedPaths = append(out.FailedPaths, result.Path)
		}
	}
	out.Applied = out.FailedFiles == 0 && out.WrittenFiles == len(entries)
	out.Summary = fmt.Sprintf("Requested %d files: %d written, %d failed, %d skipped", len(entries), out.WrittenFiles, out.FailedFiles, out.SkippedFiles)
	content, err := json.Marshal(out)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error marshaling result: %v", err)}, nil
	}
	return Result{IsError: out.FailedFiles > 0, Content: string(content)}, nil
}

type multiFileEditEntry struct {
	Path  string
	Edits []textEdit
}

func (t MultiFileEdit) parseInput(input json.RawMessage) (string, []multiFileEditEntry, *Result) {
	var args struct {
		Mode  string `json:"mode"`
		Files []struct {
			Path  string `json:"path"`
			Edits []struct {
				OldText string `json:"old_text"`
				NewText string `json:"new_text"`
			} `json:"edits"`
		} `json:"files"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", nil, &Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}
	}
	mode := args.Mode
	if mode == "" {
		mode = "atomic"
	}
	if mode != "atomic" && mode != "partial_success" {
		return "", nil, &Result{IsError: true, Content: fmt.Sprintf("unsupported mode %q", mode)}
	}
	if len(args.Files) == 0 {
		return "", nil, &Result{IsError: true, Content: "files array must not be empty"}
	}
	if len(args.Files) > maxMultiFileEditFiles {
		return "", nil, &Result{IsError: true, Content: fmt.Sprintf("too many files: got %d, max %d. Split the edit into smaller batches.", len(args.Files), maxMultiFileEditFiles)}
	}
	totalEdits := 0
	totalPayloadBytes := 0
	entries := make([]multiFileEditEntry, len(args.Files))
	seen := map[string]struct{}{}
	for i, f := range args.Files {
		path, err := cleanAbsolutePath(f.Path)
		if err != nil {
			return "", nil, &Result{IsError: true, Content: fmt.Sprintf("files[%d]: %v", i, err)}
		}
		if _, ok := seen[path]; ok {
			return "", nil, &Result{IsError: true, Content: fmt.Sprintf("files[%d]: duplicate path %s", i, path)}
		}
		seen[path] = struct{}{}
		if len(f.Edits) == 0 {
			return "", nil, &Result{IsError: true, Content: fmt.Sprintf("files[%d].edits must not be empty", i)}
		}
		if len(f.Edits) > maxMultiFileEditEditsPerFile {
			return "", nil, &Result{IsError: true, Content: fmt.Sprintf("files[%d]: too many edits: got %d, max %d", i, len(f.Edits), maxMultiFileEditEditsPerFile)}
		}
		entry := multiFileEditEntry{Path: path, Edits: make([]textEdit, len(f.Edits))}
		for j, edit := range f.Edits {
			totalEdits++
			totalPayloadBytes += len(edit.OldText) + len(edit.NewText)
			if edit.OldText == "" {
				return "", nil, &Result{IsError: true, Content: fmt.Sprintf("files[%d].edits[%d]: old_text must not be empty", i, j)}
			}
			entry.Edits[j] = textEdit{OldText: edit.OldText, NewText: edit.NewText}
		}
		entries[i] = entry
	}
	if totalEdits > maxMultiFileEditTotalEdits {
		return "", nil, &Result{IsError: true, Content: fmt.Sprintf("too many edits: got %d, max %d. Split the edit into smaller batches.", totalEdits, maxMultiFileEditTotalEdits)}
	}
	if totalPayloadBytes > maxMultiFileEditPayloadBytes {
		return "", nil, &Result{IsError: true, Content: fmt.Sprintf("combined old_text/new_text payload too large (%d bytes, max %d). Split the edit into smaller batches.", totalPayloadBytes, maxMultiFileEditPayloadBytes)}
	}
	return mode, entries, nil
}

func (t MultiFileEdit) planEntries(entries []multiFileEditEntry) ([]PlannedFileEdit, []MultiFileEditFileResult, bool) {
	plans := make([]PlannedFileEdit, 0, len(entries))
	results := make([]MultiFileEditFileResult, len(entries))
	hasFailures := false
	for i, entry := range entries {
		results[i].Path = entry.Path
		if t.SandboxCheck != nil && !t.SandboxCheck(entry.Path) {
			results[i].Status = "error"
			results[i].Error = "Error: path not allowed by sandbox policy"
			hasFailures = true
			continue
		}
		data, err := os.ReadFile(entry.Path)
		if err != nil {
			results[i].Status = "error"
			results[i].Error = fmt.Sprintf("error reading file: %v", err)
			hasFailures = true
			continue
		}
		oldContent := string(data)
		newContent, applied, msg := planTextEdits(oldContent, entry.Edits)
		if msg != "" {
			results[i].Status = "error"
			results[i].Error = msg
			hasFailures = true
			continue
		}
		plans = append(plans, PlannedFileEdit{
			Path:             entry.Path,
			OldContent:       oldContent,
			NewContent:       newContent,
			AppliedEditCount: applied,
		})
	}
	return plans, results, hasFailures
}

func applyAtomicPlans(plans []PlannedFileEdit) (string, error) {
	written := make([]PlannedFileEdit, 0, len(plans))
	for _, plan := range plans {
		if err := atomicWriteFile(plan.Path, []byte(plan.NewContent), 0644); err != nil {
			for i := len(written) - 1; i >= 0; i-- {
				_ = atomicWriteFile(written[i].Path, []byte(written[i].OldContent), 0644)
			}
			return plan.Path, fmt.Errorf("error writing file: %v", err)
		}
		written = append(written, plan)
	}
	return "", nil
}

func formatMultiFileReadFileBlock(path, text string) string {
	return fmt.Sprintf("=== FILE: %s ===\n%s[end file]\n", path, ensureTrailingNewline(text))
}

func formatMultiFileReadErrorBlock(path, errText string) string {
	return fmt.Sprintf("=== ERROR: %s ===\n%s\n[end error]\n", path, strings.TrimSpace(errText))
}

func formatMultiFileReadSkippedBlock(path string) string {
	return fmt.Sprintf("=== FILE: %s ===\n[skipped: combined output limit reached; split into a smaller batch]\n[end file]\n", path)
}

func ensureTrailingNewline(s string) string {
	if strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}

func countMultiFileEditStatus(results []MultiFileEditFileResult, status string) int {
	count := 0
	for _, result := range results {
		if result.Status == status {
			count++
		}
	}
	return count
}

func collectMultiFileEditPaths(results []MultiFileEditFileResult, status string) []string {
	var paths []string
	for _, result := range results {
		if result.Status == status && result.Path != "" {
			paths = append(paths, result.Path)
		}
	}
	return paths
}

func containsStringValue(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
