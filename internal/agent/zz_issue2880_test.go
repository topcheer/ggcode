package agent

// #2880: multi_file_edit old_text/new_text must follow the same two-tier
// thresholds as edit_file and multi_edit_file (4KB warn / 16KB severe).
// analyzeMultiFileEditSize previously warned only at the severe tier.

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAnalyzeArgSize_MultiFileEditThresholdTiers(t *testing.T) {
	mkArgs := func(oldLen int) []byte {
		args, _ := json.Marshal(map[string]interface{}{
			"files": []map[string]interface{}{
				{
					"path": "/tmp/a.go",
					"edits": []map[string]interface{}{
						{"old_text": strings.Repeat("c", oldLen), "new_text": "x"},
					},
				},
			},
		})
		return args
	}

	cases := []struct {
		name    string
		oldLen  int
		wantHit bool
		wantSub string // substring expected when wantHit
	}{
		{"3KB below warn tier", 3 * 1024, false, ""},
		{"5KB warn tier", 5 * 1024, true, "consider using shorter line-number anchors"},
		{"20KB severe tier", 20 * 1024, true, "use concise line-number anchors"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hint := analyzeArgSize("multi_file_edit", mkArgs(tc.oldLen))
			if !tc.wantHit {
				if hint != "" {
					t.Fatalf("expected no hint for %d-byte old_text, got %q", tc.oldLen, hint)
				}
				return
			}
			if hint == "" {
				t.Fatalf("expected hint for %d-byte old_text, got empty", tc.oldLen)
			}
			if !strings.Contains(hint, tc.wantSub) {
				t.Errorf("hint should contain %q, got %q", tc.wantSub, hint)
			}
			if !strings.Contains(hint, "old_text") {
				t.Errorf("hint should mention old_text, got %q", hint)
			}
		})
	}
}

// Cross-tool parity: the same 6KB old_text must warn in all three edit tools
// (#2880 core complaint — multi_file_edit was silent where siblings warned).
func TestAnalyzeArgSize_EditToolThresholdParity(t *testing.T) {
	const bigLen = 6 * 1024
	big := strings.Repeat("p", bigLen)

	mfeArgs, _ := json.Marshal(map[string]interface{}{
		"files": []map[string]interface{}{
			{"path": "/tmp/a.go", "edits": []map[string]interface{}{
				{"old_text": big, "new_text": "x"},
			}},
		},
	})
	mefArgs, _ := json.Marshal(map[string]interface{}{
		"file_path": "/tmp/a.go",
		"edits":     []map[string]interface{}{{"old_text": big, "new_text": "x"}},
	})
	efArgs, _ := json.Marshal(map[string]interface{}{
		"file_path": "/tmp/a.go", "old_text": big, "new_text": "x",
	})

	for _, tc := range []struct {
		tool string
		args []byte
	}{
		{"multi_file_edit", mfeArgs},
		{"multi_edit_file", mefArgs},
		{"edit_file", efArgs},
	} {
		hint := analyzeArgSize(tc.tool, tc.args)
		if hint == "" {
			t.Errorf("%s: 6KB old_text must produce a hint (parity with siblings)", tc.tool)
		}
	}
}
