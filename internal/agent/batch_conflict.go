package agent

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

const firstConflictWarning = `[batch conflict detected] This file is also targeted by %d other edit(s) in the same response.
` +
	`When multiple edits are applied to the same file in one batch, the file changes after each edit, so ` +
	`subsequent edits may fail because their old_text no longer matches. To avoid this:
` +
	`- For multiple non-overlapping edits to the same file, use multi_edit_file to apply them atomically.
` +
	`- For dependent edits, wait for this edit to complete before sending the next one.`

const subsequentConflictWarning = `[batch conflict detected] This file was already modified by an earlier edit in this response (tool call #%d).
` +
	`The file content has changed since you composed this edit, so old_text may not match anymore. ` +
	`Re-read the file if this edit fails, or consolidate same-file edits using multi_edit_file.`

func detectBatchEditConflicts(toolCalls []provider.ToolCallDelta) map[int]string {
	if len(toolCalls) < 2 {
		return nil
	}

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

		firstIdx := indices[0]
		otherCount := len(indices) - 1
		warnings[firstIdx] = fmt.Sprintf(firstConflictWarning, otherCount)

		for k := 1; k < len(indices); k++ {
			warnings[indices[k]] = fmt.Sprintf(subsequentConflictWarning, firstIdx+1)
		}
	}

	if len(warnings) > 0 {
		debug.Log("batch_conflict", "detected %d conflicting tool calls in batch of %d", len(warnings), len(toolCalls))
	}
	return warnings
}

func extractFilePathsForConflict(tc provider.ToolCallDelta) []string {
	if len(tc.Arguments) == 0 {
		return nil
	}
	var args map[string]any
	if json.Unmarshal(tc.Arguments, &args) != nil {
		return nil
	}

	var paths []string
	appendStr := func(key string) {
		if p, ok := args[key].(string); ok {
			paths = append(paths, p)
		}
	}
	appendFromList := func(listKey, pathKey string) {
		items, ok := args[listKey].([]any)
		if !ok {
			return
		}
		for _, item := range items {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if p, ok := m[pathKey].(string); ok {
				paths = append(paths, p)
			}
		}
	}

	switch tc.Name {
	case "write_file", "edit_file":
		appendStr("path")
		appendStr("file_path")
	case "multi_edit_file":
		appendStr("file_path")

		appendFromList("edits", "path")
	case "multi_file_edit", "multi_file_write":
		appendFromList("files", "path")
	case "notebook_edit":
		appendStr("notebook_path")
	}
	return paths
}

func normalizeBatchPath(p string) string {
	p = strings.TrimSpace(p)
	if !batchPathFoldActive() {
		return p
	}
	return asciiFoldLower(p)
}

func batchPathFoldActive() bool {
	switch runtime.GOOS {
	case "darwin", "windows":
		return true
	}
	return false
}

func asciiFoldLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}
