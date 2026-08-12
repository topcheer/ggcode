package permission

import (
	"encoding/json"
	"testing"
)

func mustJSON(t *testing.T, v map[string]interface{}) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPathSignature(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"src/foo.go", "src/*.go"},
		{"src/bar.go", "src/*.go"},
		{"src/sub/baz.go", "src/sub/*.go"},
		{"internal/pkg/handler_test.go", "internal/pkg/*_test.go"},
		{"README.md", "*.md"},
		{"", ""},
	}
	for _, tt := range tests {
		got := pathSignature(tt.input)
		if got != tt.want {
			t.Errorf("pathSignature(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestCommandSignature(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"go build ./...", "go build"},
		{"git status", "git status"},
		{"ls -la", "ls -la"},
		{"npm", "npm"},
		{"", ""},
	}
	for _, tt := range tests {
		got := commandSignature(tt.input)
		if got != tt.want {
			t.Errorf("commandSignature(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestMakeKey_FileTool(t *testing.T) {
	input := mustJSON(t, map[string]interface{}{"file_path": "/project/src/main.go"})
	key, ok := MakeKey("edit_file", input)
	if !ok {
		t.Fatal("expected ok=true")
	}
	want := "edit_file:/project/src/*.go"
	if key != want {
		t.Errorf("MakeKey = %q, want %q", key, want)
	}
}

func TestMakeKey_CommandTool(t *testing.T) {
	input := mustJSON(t, map[string]interface{}{"command": "go test ./..."})
	key, ok := MakeKey("run_command", input)
	if !ok {
		t.Fatal("expected ok=true")
	}
	want := "run_command:go test"
	if key != want {
		t.Errorf("MakeKey = %q, want %q", key, want)
	}
}

func TestMakeKey_NoPath(t *testing.T) {
	input := mustJSON(t, map[string]interface{}{"query": "something"})
	key, ok := MakeKey("code_search", input)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if key != "code_search" {
		t.Errorf("MakeKey = %q, want %q", key, "code_search")
	}
}

func TestApprovalMemory_AutoApproveAfterThreshold(t *testing.T) {
	am := NewApprovalMemory()
	input := mustJSON(t, map[string]interface{}{"file_path": "/project/src/handler.go"})

	// Initially not auto-approved.
	if am.ShouldAutoApprove("edit_file", input) {
		t.Fatal("should not auto-approve initially")
	}

	// Approve 3 times.
	for i := 0; i < 3; i++ {
		am.RecordApproval("edit_file", input)
	}

	// Now should be auto-approved.
	if !am.ShouldAutoApprove("edit_file", input) {
		t.Fatal("should auto-approve after 3 approvals")
	}

	// A different file in the same directory should also be auto-approved
	// (same path signature).
	input2 := mustJSON(t, map[string]interface{}{"file_path": "/project/src/other.go"})
	if !am.ShouldAutoApprove("edit_file", input2) {
		t.Fatal("should auto-approve same path-signature")
	}
}

func TestApprovalMemory_DenyResetsCount(t *testing.T) {
	am := NewApprovalMemory()
	input := mustJSON(t, map[string]interface{}{"file_path": "/project/src/main.go"})

	// Approve twice.
	am.RecordApproval("edit_file", input)
	am.RecordApproval("edit_file", input)

	// Deny - resets count.
	am.RecordDeny("edit_file", input)

	// Should not be auto-approved.
	if am.ShouldAutoApprove("edit_file", input) {
		t.Fatal("should not auto-approve after deny reset")
	}

	// Approve 3 more times to re-activate.
	for i := 0; i < 3; i++ {
		am.RecordApproval("edit_file", input)
	}
	if !am.ShouldAutoApprove("edit_file", input) {
		t.Fatal("should auto-approve after 3 more approvals post-deny")
	}
}

func TestApprovalMemory_DifferentToolsSeparate(t *testing.T) {
	am := NewApprovalMemory()
	input := mustJSON(t, map[string]interface{}{"file_path": "/project/src/main.go"})

	// Approve edit_file 3 times.
	for i := 0; i < 3; i++ {
		am.RecordApproval("edit_file", input)
	}

	// write_file should NOT be auto-approved (different tool).
	if am.ShouldAutoApprove("write_file", input) {
		t.Fatal("different tool should not be auto-approved")
	}
}

func TestApprovalMemory_Reset(t *testing.T) {
	am := NewApprovalMemory()
	input := mustJSON(t, map[string]interface{}{"command": "go test ./..."})

	for i := 0; i < 3; i++ {
		am.RecordApproval("run_command", input)
	}
	if !am.ShouldAutoApprove("run_command", input) {
		t.Fatal("should be auto-approved before reset")
	}

	am.Reset()

	if am.ShouldAutoApprove("run_command", input) {
		t.Fatal("should not be auto-approved after reset")
	}
}

func TestApprovalMemory_NilSafe(t *testing.T) {
	var am *ApprovalMemory
	input := mustJSON(t, map[string]interface{}{"file_path": "/x.go"})

	// Should not panic on nil receiver.
	if am.ShouldAutoApprove("edit_file", input) {
		t.Fatal("nil receiver should not auto-approve")
	}
	am.RecordApproval("edit_file", input)
	am.RecordDeny("edit_file", input)
	am.Reset()
}

func TestApprovalMemory_AutoApprovedKeys(t *testing.T) {
	am := NewApprovalMemory()
	input1 := mustJSON(t, map[string]interface{}{"file_path": "/project/src/a.go"})
	input2 := mustJSON(t, map[string]interface{}{"command": "npm test"})

	for i := 0; i < 3; i++ {
		am.RecordApproval("edit_file", input1)
		am.RecordApproval("run_command", input2)
	}

	keys := am.AutoApprovedKeys()
	if len(keys) != 2 {
		t.Fatalf("expected 2 auto-approved keys, got %d: %v", len(keys), keys)
	}
}
