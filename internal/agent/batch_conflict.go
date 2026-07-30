package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// Batch Edit Conflict Detection — detects when the LLM emits multiple
// file-editing tool calls targeting the same file in a single batch.
//
// Problem: When the model returns multiple edit_file / multi_edit_file calls
// for the same file in one response, the edits are executed sequentially.
// The first edit modifies the file, so the second edit's old_text may no
// longer match — causing a confusing failure that the model struggles to
// diagnose. This wastes iterations and triggers cascading recovery hooks
// (edit_fail_recovery, error_streak, repetition_tracker) unnecessarily.
//
// Solution: Before the sequential loop, scan all file-editing tool calls in
// the batch and build a conflict map. When a file is targeted by 2+ edits,
// inject a warning into the tool results so the model understands:
//   - Why the second edit may fail (file was already modified)
//   - How to fix it: use multi_edit_file for multiple edits to the same file
//
// Competitor analysis:
//   - Claude Code: warns when parallel edits collide; suggests consolidation
//   - Cursor: detects overlapping edits and asks for clarification
//   - Cline/OpenHands: serializes same-file writes with explicit warnings
//   - Aider: uses a unified edit protocol that batches same-file edits

// batchFileConflictWarning is injected into the result of the first tool call
// in a batch that targets a file also edited by another call in the same batch.
const firstConflictWarning = `[batch conflict detected] This file is also targeted by %d other edit(s) in the same response.
` +
	`When multiple edits are applied to the same file in one batch, the file changes after each edit, so ` +
	`subsequent edits may fail because their old_text no longer matches. To avoid this:
` +
	`- For multiple non-overlapping edits to the same file, use multi_edit_file to apply them atomically.
` +
	`- For dependent edits, wait for this edit to complete before sending the next one.`

// batchFileConflictSubsequent is injected into subsequent tool calls targeting
// a file that was already modified earlier in this batch.
const subsequentConflictWarning = `[batch conflict detected] This file was already modified by an earlier edit in this response (tool call #%d).
` +
	`The file content has changed since you composed this edit, so old_text may not match anymore. ` +
	`Re-read the file if this edit fails, or consolidate same-file edits using multi_edit_file.`

// detectBatchEditConflicts scans all tool calls in the batch and identifies
// file-editing calls that target the same file path. Returns a map from tool
// call index to a warning string for each conflicting call.
//
// For each file with multiple edit targets:
//   - The first call gets firstConflictWarning (tells the model others are coming)
//   - Subsequent calls get subsequentConflictWarning (tells the model the file changed)
//
// This is a pure function — no state, no side effects.
func detectBatchEditConflicts(toolCalls []provider.ToolCallDelta) map[int]string {
	if len(toolCalls) < 2 {
		return nil
	}

	// Map: normalized file path → ordered list of tool-call indices targeting it.
	fileToIndices := make(map[string][]int)

	for i, tc := range toolCalls {
		if !fileEditingTools[tc.Name] && tc.Name != "notebook_edit" {
			continue
		}
		paths := extractFilePathsForConflict(tc)
		for _, p := range paths {
			np := normalizeBatchPath(p)
			fileToIndices[np] = append(fileToIndices[np], i)
		}
	}

	warnings := make(map[int]string)
	for _, indices := range fileToIndices {
		if len(indices) < 2 {
			continue
		}
		// indices[0] is the first call to this file — warn about upcoming conflicts.
		firstIdx := indices[0]
		otherCount := len(indices) - 1
		warnings[firstIdx] = fmt.Sprintf(firstConflictWarning, otherCount)

		// Subsequent calls — warn that the file was already modified.
		for k := 1; k < len(indices); k++ {
			warnings[indices[k]] = fmt.Sprintf(subsequentConflictWarning, firstIdx+1)
		}
	}

	if len(warnings) > 0 {
		debug.Log("batch_conflict", "detected %d conflicting tool calls in batch of %d", len(warnings), len(toolCalls))
	}
	return warnings
}

// extractFilePathsForConflict extracts target file paths from a file-editing
// tool call. It handles both single-file tools (edit_file, write_file) and
// multi-file tools (multi_edit_file, multi_file_write, multi_file_edit).
func extractFilePathsForConflict(tc provider.ToolCallDelta) []string {
	if len(tc.Arguments) == 0 {
		return nil
	}
	var args map[string]any
	if json.Unmarshal(tc.Arguments, &args) != nil {
		return nil
	}

	var paths []string
	switch tc.Name {
	case "write_file", "edit_file":
		if p, ok := args["path"].(string); ok {
			paths = append(paths, p)
		}
		if p, ok := args["file_path"].(string); ok {
			paths = append(paths, p)
		}
	case "multi_edit_file":
		if p, ok := args["file_path"].(string); ok {
			paths = append(paths, p)
		}
		// multi_edit_file edits array also contains path in some variants.
		if edits, ok := args["edits"].([]any); ok {
			for _, e := range edits {
				if em, ok := e.(map[string]any); ok {
					if p, ok := em["path"].(string); ok {
						paths = append(paths, p)
					}
				}
			}
		}
	case "multi_file_edit", "multi_file_write":
		if files, ok := args["files"].([]any); ok {
			for _, f := range files {
				if fm, ok := f.(map[string]any); ok {
					if p, ok := fm["path"].(string); ok {
						paths = append(paths, p)
					}
				}
			}
		}
	case "notebook_edit":
		if p, ok := args["notebook_path"].(string); ok {
			paths = append(paths, p)
		}
	}
	return paths
}

// normalizeBatchPath lowercases and trims the path for case-insensitive matching.
// On case-insensitive filesystems (macOS HFS+, Windows NTFS), Foo.go and
// foo.go refer to the same file.
func normalizeBatchPath(p string) string {
	return strings.ToLower(strings.TrimSpace(p))
}
