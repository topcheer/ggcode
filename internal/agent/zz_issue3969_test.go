package agent

import (
	"encoding/json"
	"testing"
)

func filesEditedBy(t *testing.T, toolName string, raw string) map[string]bool {
	t.Helper()
	s := &RunStats{}
	extractPathsFromToolCall(toolName, json.RawMessage(raw), s)
	got := map[string]bool{}
	for _, p := range s.FilesEdited {
		got[p] = true
	}
	return got
}

func TestIssue3969FileOpsMoveRecordsBothHalves(t *testing.T) {
	got := filesEditedBy(t, "file_ops",
		`{"operations":[{"action":"move","source":"a/utils.go","destination":"b/helpers.go"}]}`)
	for _, want := range []string{"a/utils.go", "b/helpers.go"} {
		if !got[want] {
			t.Fatalf("file_ops move: %s missing from FilesEdited (got %v) - plan-drift corpus blind to the rename", want, got)
		}
	}
}

func TestIssue3969FileOpsMkdirRecordsSource(t *testing.T) {
	got := filesEditedBy(t, "file_ops",
		`{"operations":[{"action":"mkdir","source":"pkg/newdir"}]}`)
	if !got["pkg/newdir"] {
		t.Fatalf("file_ops mkdir: pkg/newdir missing from FilesEdited (got %v)", got)
	}
}

func TestIssue3969BatchReplaceRecordsFiles(t *testing.T) {
	got := filesEditedBy(t, "batch_replace",
		`{"pattern":"OldName","replacement":"NewName","files":["internal/a.go","cmd/b.go"]}`)
	for _, want := range []string{"internal/a.go", "cmd/b.go"} {
		if !got[want] {
			t.Fatalf("batch_replace: %s missing from FilesEdited (got %v)", want, got)
		}
	}
}

func TestIssue3969FileOpsMalformedNoPanic(t *testing.T) {
	for _, tc := range []struct{ name, raw string }{
		{"bad json", `{"operations":[`},
		{"operations not array", `{"operations":"x"}`},
		{"op not object", `{"operations":["x"]}`},
		{"empty source", `{"operations":[{"action":"mkdir","source":""}]}`},
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%s: panicked: %v", tc.name, r)
				}
			}()
			s := &RunStats{}
			extractPathsFromToolCall("file_ops", json.RawMessage(tc.raw), s)
			if tc.name == "empty source" && len(s.FilesEdited) != 0 {
				t.Fatalf("empty source recorded into FilesEdited: %v", s.FilesEdited)
			}
		}()
	}
}
