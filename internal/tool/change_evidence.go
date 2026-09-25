package tool

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// Change-evidence helpers for round-level IM/desktop summaries.
//
// Research basis: OverclaimBench (arXiv:2609.20812) found that agents'
// final responses frequently misreport what was actually done (80% of
// incomplete review runs were misleading), and AgentForge-Bench
// (arXiv:2609.23953) measured 41% of wrong edits reported as done.
// In IM/desktop channels the final round message is often the ONLY thing
// the user sees, so it should carry a transcript-grounded receipt of which
// files were actually modified (derived from real tool calls, not from
// the model's own wording). Parity note: the TUI already shows a changed
// files summary (internal/tui/change_summary.go); this closes the same
// gap for IM/desktop final messages.

// editedFileToolArgs are JSON arg keys that hold a file path on write-class
// tools (write_file: path, edit_file/multi_edit_file: file_path,
// notebook_edit: notebook_path).
var editedFilePathKeys = []string{"path", "file_path", "notebook_path"}

// editedFileTools are built-in tools whose successful execution modifies a
// file on disk. Path arguments are extracted for the change receipt.
var editedFileTools = map[string]bool{
	"write_file":      true,
	"edit_file":       true,
	"multi_edit_file": true,
	"multi_file_edit": true,
	"batch_replace":   true,
	"notebook_edit":   true,
}

// ExtractEditedFilePaths returns the file paths a write-class tool call
// targets, based on its raw JSON arguments. Non-write tools return nil.
// Tolerant of the three arg shapes in use:
//   - single path string field (path / file_path / notebook_path)
//   - "files": ["a.go", "b.go"]            (batch_replace)
//   - "files": [{"path": "a.go"}, ...]     (multi_file_edit)
func ExtractEditedFilePaths(toolName, rawArgs string) []string {
	if !editedFileTools[strings.TrimSpace(toolName)] {
		return nil
	}
	rawArgs = strings.TrimSpace(rawArgs)
	if rawArgs == "" || rawArgs == "null" {
		return nil
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return nil
	}

	var out []string
	seen := make(map[string]bool)
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}

	for _, key := range editedFilePathKeys {
		if v, ok := args[key]; ok {
			var p string
			if json.Unmarshal(v, &p) == nil {
				add(p)
			}
		}
	}
	if v, ok := args["files"]; ok {
		var list []json.RawMessage
		if json.Unmarshal(v, &list) == nil {
			for _, item := range list {
				var p string
				if json.Unmarshal(item, &p) == nil {
					add(p)
					continue
				}
				var obj struct {
					Path string `json:"path"`
				}
				if json.Unmarshal(item, &obj) == nil {
					add(obj.Path)
				}
			}
		}
	}
	return out
}

// changedFilesFooterLimit caps how many paths are listed before collapsing
// into "+N more" so IM messages stay compact (WeChat/QQ message budgets).
const changedFilesFooterLimit = 8

// FormatChangedFilesFooter renders a compact, transcript-grounded receipt of
// the files modified during the round. lang is "zh-CN", "en" or "" (en).
// Returns "" when no files were edited, so callers can append it to the
// final round message unconditionally.
func FormatChangedFilesFooter(lang string, files []string) string {
	files = dedupeFiles(files)
	if len(files) == 0 {
		return ""
	}

	shown := files
	more := 0
	if len(files) > changedFilesFooterLimit {
		shown = files[:changedFilesFooterLimit]
		more = len(files) - changedFilesFooterLimit
	}

	label := "Changed files"
	if lang == "zh-CN" {
		label = "变更文件"
	}
	var b strings.Builder
	b.WriteString("📝 ")
	b.WriteString(label)
	b.WriteString(": ")
	b.WriteString(strings.Join(shown, ", "))
	if more > 0 {
		fmt.Fprintf(&b, " (+%d)", more)
	}
	return b.String()
}

func dedupeFiles(files []string) []string {
	seen := make(map[string]bool, len(files))
	out := make([]string, 0, len(files))
	for _, f := range files {
		f = strings.TrimSpace(f)
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, filepath.ToSlash(f))
	}
	return out
}
