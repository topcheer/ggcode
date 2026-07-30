package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDetectBatchEditConflicts_NoConflict(t *testing.T) {
	calls := []provider.ToolCallDelta{
		{Name: "edit_file", Arguments: mustJSON(t, map[string]string{"file_path": "a.go"})},
		{Name: "edit_file", Arguments: mustJSON(t, map[string]string{"file_path": "b.go"})},
	}
	warnings := detectBatchEditConflicts(calls)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestDetectBatchEditConflicts_SingleCall(t *testing.T) {
	calls := []provider.ToolCallDelta{
		{Name: "edit_file", Arguments: mustJSON(t, map[string]string{"file_path": "a.go"})},
	}
	warnings := detectBatchEditConflicts(calls)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for single call, got %d", len(warnings))
	}
}

func TestDetectBatchEditConflicts_SameFileEditTwice(t *testing.T) {
	calls := []provider.ToolCallDelta{
		{Name: "edit_file", Arguments: mustJSON(t, map[string]string{"file_path": "a.go", "old_text": "foo", "new_text": "bar"})},
		{Name: "edit_file", Arguments: mustJSON(t, map[string]string{"file_path": "a.go", "old_text": "baz", "new_text": "qux"})},
	}
	warnings := detectBatchEditConflicts(calls)
	if len(warnings) != 2 {
		t.Fatalf("expected 2 warnings (first + subsequent), got %d", len(warnings))
	}
	// Index 0 = first conflict warning
	if w, ok := warnings[0]; !ok {
		t.Fatal("expected warning for index 0")
	} else if !strings.Contains(w, "batch conflict") {
		t.Fatalf("warning[0] unexpected: %s", w)
	}
	// Index 1 = subsequent conflict warning
	if w, ok := warnings[1]; !ok {
		t.Fatal("expected warning for index 1")
	} else if !strings.Contains(w, "already modified") {
		t.Fatalf("warning[1] unexpected: %s", w)
	}
}

func TestDetectBatchEditConflicts_WriteThenEdit(t *testing.T) {
	calls := []provider.ToolCallDelta{
		{Name: "write_file", Arguments: mustJSON(t, map[string]string{"path": "config.yaml"})},
		{Name: "edit_file", Arguments: mustJSON(t, map[string]string{"file_path": "config.yaml"})},
	}
	warnings := detectBatchEditConflicts(calls)
	if len(warnings) != 2 {
		t.Fatalf("expected 2 warnings for write+edit on same file, got %d", len(warnings))
	}
}

func TestDetectBatchEditConflicts_MultiFileEdit(t *testing.T) {
	calls := []provider.ToolCallDelta{
		{Name: "multi_file_edit", Arguments: mustJSON(t, map[string]any{
			"files": []map[string]string{{"path": "x.go"}, {"path": "y.go"}},
		})},
		{Name: "edit_file", Arguments: mustJSON(t, map[string]string{"file_path": "y.go"})},
	}
	warnings := detectBatchEditConflicts(calls)
	// y.go appears in both calls → conflict.
	if len(warnings) != 2 {
		t.Fatalf("expected 2 warnings (multi_file_edit and edit_file both touch y.go), got %d", len(warnings))
	}
}

func TestDetectBatchEditConflicts_IgnoresReadOnlyTools(t *testing.T) {
	calls := []provider.ToolCallDelta{
		{Name: "read_file", Arguments: mustJSON(t, map[string]string{"path": "a.go"})},
		{Name: "read_file", Arguments: mustJSON(t, map[string]string{"path": "a.go"})},
	}
	warnings := detectBatchEditConflicts(calls)
	if len(warnings) != 0 {
		t.Fatalf("read-only tools should not trigger conflict detection, got %d", len(warnings))
	}
}

func TestDetectBatchEditConflicts_CaseInsensitive(t *testing.T) {
	calls := []provider.ToolCallDelta{
		{Name: "edit_file", Arguments: mustJSON(t, map[string]string{"file_path": "Foo.go"})},
		{Name: "edit_file", Arguments: mustJSON(t, map[string]string{"file_path": "foo.go"})},
	}
	warnings := detectBatchEditConflicts(calls)
	if len(warnings) != 2 {
		t.Fatalf("expected conflict for case-insensitive paths, got %d", len(warnings))
	}
}

func TestDetectBatchEditConflicts_ThreeCallsSameFile(t *testing.T) {
	calls := []provider.ToolCallDelta{
		{Name: "edit_file", Arguments: mustJSON(t, map[string]string{"file_path": "main.go"})},
		{Name: "edit_file", Arguments: mustJSON(t, map[string]string{"file_path": "main.go"})},
		{Name: "edit_file", Arguments: mustJSON(t, map[string]string{"file_path": "main.go"})},
	}
	warnings := detectBatchEditConflicts(calls)
	if len(warnings) != 3 {
		t.Fatalf("expected 3 warnings for 3 conflicting calls, got %d", len(warnings))
	}
	// First call should mention 2 others.
	if !strings.Contains(warnings[0], "2 other edit") {
		t.Fatalf("first warning should mention 2 others: %s", warnings[0])
	}
}

func TestDetectBatchEditConflicts_NotebookEdit(t *testing.T) {
	calls := []provider.ToolCallDelta{
		{Name: "notebook_edit", Arguments: mustJSON(t, map[string]string{"notebook_path": "analysis.ipynb"})},
		{Name: "notebook_edit", Arguments: mustJSON(t, map[string]string{"notebook_path": "analysis.ipynb"})},
	}
	warnings := detectBatchEditConflicts(calls)
	if len(warnings) != 2 {
		t.Fatalf("expected 2 warnings for conflicting notebook edits, got %d", len(warnings))
	}
}

func TestDetectBatchEditConflicts_EmptyAndInvalidArgs(t *testing.T) {
	calls := []provider.ToolCallDelta{
		{Name: "edit_file", Arguments: nil},
		{Name: "edit_file", Arguments: json.RawMessage(`{invalid}`)},
		{Name: "edit_file", Arguments: mustJSON(t, map[string]string{"file_path": "a.go"})},
	}
	warnings := detectBatchEditConflicts(calls)
	// Only one valid call to a.go → no conflict.
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings with invalid args, got %d", len(warnings))
	}
}

func TestExtractFilePathsForConflict_AllToolTypes(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
		want int
	}{
		{"edit_file", map[string]any{"file_path": "a.go"}, 1},
		{"write_file", map[string]any{"path": "b.go"}, 1},
		{"multi_edit_file", map[string]any{"file_path": "c.go"}, 1},
		{"notebook_edit", map[string]any{"notebook_path": "d.ipynb"}, 1},
		{"read_file", map[string]any{"path": "e.go"}, 0}, // not an editing tool
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := provider.ToolCallDelta{Name: tt.name, Arguments: mustJSON(t, tt.args)}
			paths := extractFilePathsForConflict(tc)
			if len(paths) != tt.want {
				t.Fatalf("%s: expected %d paths, got %d (%v)", tt.name, tt.want, len(paths), paths)
			}
		})
	}
}
