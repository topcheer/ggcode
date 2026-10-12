package agent

// Tests for #4007: extractEditedPaths must cover every member of the
// canonical sourceMutatingTools superset. Before the fix, multi_edit_file,
// batch_replace, lsp_rename and file_ops passed the edit-tool gates at both
// call sites (agent.go MarkDirty + fileChurn/tunnelVision/prematureCommit
// wiring) but yielded zero paths, silently starving every downstream
// detector. This test walks the superset so the hand-written switch can
// never drift from verify_hint.go's canonical map again.

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// issue4007SampleArgs carries one realistic argument payload per
// source-mutating tool, matching the registered tool schemas.
var issue4007SampleArgs = map[string]string{
	"edit_file":        `{"file_path": "a.go", "old_text": "x", "new_text": "y"}`,
	"write_file":       `{"path": "b.go", "content": "..."}`,
	"multi_edit_file":  `{"file_path": "c.go", "edits": []}`,
	"multi_file_edit":  `{"files": [{"path": "d.go", "old_text": "x", "new_text": "y"}]}`,
	"multi_file_write": `{"files": [{"path": "e.go", "content": "..."}]}`,
	"batch_replace":    `{"pattern": "x", "replacement": "y", "files": ["f.go", "g.go"]}`,
	"lsp_rename":       `{"path": "h.go", "line": 1, "character": 1, "new_name": "Renamed"}`,
	"file_ops":         `{"operations": [{"action": "move", "source": "i.go", "destination": "j.go"}]}`,
	"notebook_edit":    `{"notebook_path": "n.ipynb", "operation": "add"}`,
}

// TestIssue4007ExtractEditedPathsCoversSuperset: every source-mutating tool
// must produce at least one path - the anti-drift pin.
func TestIssue4007ExtractEditedPathsCoversSuperset(t *testing.T) {
	for name := range sourceMutatingTools {
		sample, ok := issue4007SampleArgs[name]
		if !ok {
			t.Errorf("no sample args for source-mutating tool %q - extend issue4007SampleArgs", name)
			continue
		}
		paths := extractEditedPaths(provider.ToolCallDelta{Name: name, Arguments: json.RawMessage(sample)})
		if len(paths) == 0 {
			t.Errorf("extractEditedPaths(%q) returned no paths; post-edit trackers starve silently (#4007)", name)
		}
	}
}

// TestIssue4007SpecificShapes pins the exact extraction semantics added by
// the fix.
func TestIssue4007SpecificShapes(t *testing.T) {
	t.Run("batch_replace files is a string array", func(t *testing.T) {
		paths := extractEditedPaths(provider.ToolCallDelta{
			Name:      "batch_replace",
			Arguments: json.RawMessage(`{"pattern": "x", "replacement": "y", "files": ["f.go", "g.go", "h.go"]}`),
		})
		want := []string{"f.go", "g.go", "h.go"}
		if !slices.Equal(paths, want) {
			t.Errorf("batch_replace: got %v, want %v", paths, want)
		}
	})
	t.Run("file_ops move yields source and destination", func(t *testing.T) {
		paths := extractEditedPaths(provider.ToolCallDelta{
			Name: "file_ops",
			Arguments: json.RawMessage(`{"operations": [
				{"action": "move", "source": "old/x.go", "destination": "new/x.go"},
				{"action": "delete", "source": "gone.go"}
			]}`),
		})
		want := []string{"old/x.go", "new/x.go", "gone.go"}
		if !slices.Equal(paths, want) {
			t.Errorf("file_ops: got %v, want %v", paths, want)
		}
	})
	t.Run("multi_edit_file yields its target file", func(t *testing.T) {
		paths := extractEditedPaths(provider.ToolCallDelta{
			Name:      "multi_edit_file",
			Arguments: json.RawMessage(`{"file_path": "c.go", "edits": []}`),
		})
		if !slices.Equal(paths, []string{"c.go"}) {
			t.Errorf("multi_edit_file: got %v, want [c.go]", paths)
		}
	})
	t.Run("lsp_rename yields the symbol's file", func(t *testing.T) {
		paths := extractEditedPaths(provider.ToolCallDelta{
			Name:      "lsp_rename",
			Arguments: json.RawMessage(`{"path": "h.go", "line": 1, "character": 1, "new_name": "R"}`),
		})
		if !slices.Equal(paths, []string{"h.go"}) {
			t.Errorf("lsp_rename: got %v, want [h.go]", paths)
		}
	})
	t.Run("read_file is not an edit: dead branch removed", func(t *testing.T) {
		paths := extractEditedPaths(provider.ToolCallDelta{
			Name:      "read_file",
			Arguments: json.RawMessage(`{"path": "b.go"}`),
		})
		if len(paths) != 0 {
			t.Errorf("read_file: got %v, want none (extraction is for source-mutating tools only)", paths)
		}
	})
	t.Run("malformed arguments still yield nil", func(t *testing.T) {
		if p := extractEditedPaths(provider.ToolCallDelta{Name: "edit_file", Arguments: json.RawMessage(`{not-json`)}); p != nil {
			t.Errorf("malformed args: got %v, want nil", p)
		}
		if p := extractEditedPaths(provider.ToolCallDelta{Name: "edit_file"}); p != nil {
			t.Errorf("empty args: got %v, want nil", p)
		}
	})
}
