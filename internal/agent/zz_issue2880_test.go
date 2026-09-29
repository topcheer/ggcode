package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// zz_issue2880_test.go - regression probe for #2880: multi_file_edit must
// flag oversized old_text/new_text at the same 4KB warn threshold as
// edit_file and multi_edit_file; the 4KB-16KB range used to pass silently
// (only the 16KB severe threshold was checked).
func TestIssue2880MultiFileEditWarnTierAlignment(t *testing.T) {
	// 6KB old_text: above the 4KB warn threshold, below the 16KB severe
	// threshold, with total payload under 8KB so the total-size hint cannot
	// contaminate the result.
	big := strings.Repeat("x", 6*1024)
	payload, err := json.Marshal(map[string]any{
		"files": []any{
			map[string]any{
				"path": "/tmp/a.go",
				"edits": []any{
					map[string]any{"old_text": big, "new_text": "y"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(payload) >= 8*1024 {
		t.Fatalf("test construction error: payload %d bytes exceeds total-hint threshold", len(payload))
	}

	got := analyzeArgSize("multi_file_edit", payload)
	if got == "" {
		t.Fatal("#2880: multi_file_edit stayed silent for a 6KB old_text (4KB-16KB warn range) - edit_file and multi_edit_file both flag this")
	}
	if !strings.Contains(got, "old_text") {
		t.Fatalf("hint should mention the oversized field, got: %q", got)
	}
}

func TestIssue2880ThreeEditToolsConsistent(t *testing.T) {
	big := strings.Repeat("x", 6*1024)

	// Same oversized old_text through edit_file / multi_edit_file /
	// multi_file_edit: all three must produce a hint.
	editFileArgs, _ := json.Marshal(map[string]any{
		"file_path": "/tmp/a.go", "old_text": big, "new_text": "y",
	})
	multiEditArgs, _ := json.Marshal(map[string]any{
		"file_path": "/tmp/a.go",
		"edits":     []any{map[string]any{"old_text": big, "new_text": "y"}},
	})
	multiFileArgs, _ := json.Marshal(map[string]any{
		"files": []any{map[string]any{
			"path":  "/tmp/a.go",
			"edits": []any{map[string]any{"old_text": big, "new_text": "y"}},
		}},
	})

	for name, args := range map[string][]byte{
		"edit_file":       editFileArgs,
		"multi_edit_file": multiEditArgs,
		"multi_file_edit": multiFileArgs,
	} {
		if hint := analyzeArgSize(name, args); hint == "" {
			t.Errorf("tool %s: expected a warn-tier hint for 6KB old_text, got none", name)
		}
	}
}
