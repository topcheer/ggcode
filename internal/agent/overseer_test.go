package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOverseer_TooFewCalls(t *testing.T) {
	o := newOverseerState()
	for i := 0; i < overseerInterval-1; i++ {
		o.recordToolCall("read_file", false, "/some/path.go")
	}
	msg := o.analyze(overseerInterval - 1)
	if msg != "" {
		t.Fatalf("expected no intervention with < overseerInterval calls, got: %s", msg)
	}
}

func TestOverseer_ToolSpam(t *testing.T) {
	o := newOverseerState()
	// Call search_files enough times to exceed spamThreshold.
	// analyze() needs len(trajectory) >= overseerInterval.
	for i := 0; i < overseerInterval; i++ {
		o.recordToolCall("search_files", false, "")
	}
	msg := o.analyze(overseerInterval)
	if msg == "" {
		t.Fatal("expected tool spam intervention")
	}
	if !strings.Contains(msg, "search_files") {
		t.Fatalf("expected spam message to mention the tool name, got: %s", msg)
	}
}

func TestOverseer_ReadOnlyStall_MixedReadOnlyTools(t *testing.T) {
	o := newOverseerState()
	// Use different read-only tools so tool-spam doesn't fire first.
	// Need stallThreshold consecutive read-only calls.
	readOnlyTools := []string{"read_file", "search_files", "grep", "list_directory", "glob",
		"git_log", "git_status", "git_diff", "lsp_definition", "lsp_references",
		"web_search", "web_fetch", "lsp_symbols", "git_blame", "git_show"}
	for i := 0; i < stallThreshold; i++ {
		o.recordToolCall(readOnlyTools[i%len(readOnlyTools)], false, "/path.go")
	}
	msg := o.analyze(stallThreshold)
	if msg == "" {
		t.Fatal("expected read-only stall intervention")
	}
	// Could be stall or spam — both are valid interventions.
	// Verify it mentions something about exploration or acting.
}

func TestOverseer_FileStuck(t *testing.T) {
	o := newOverseerState()
	// Read the same file fileStuckThreshold times (all read_file, which
	// triggers both spam and file-stuck — spam fires first).
	// Use different read tools to avoid spam, but same file.
	for i := 0; i < fileStuckThreshold; i++ {
		o.recordToolCall("read_file", false, "/important/file.go")
	}
	// Pad with other read-only tools to reach overseerInterval.
	for i := 0; i < overseerInterval-fileStuckThreshold; i++ {
		o.recordToolCall("grep", false, "")
	}
	msg := o.analyze(overseerInterval)
	if msg == "" {
		t.Fatal("expected intervention (file stuck or tool spam)")
	}
}

func TestOverseer_Drift(t *testing.T) {
	o := newOverseerState()
	// driftThreshold iterations without productive action.
	// Use alternating read-only tools to avoid tool-spam firing first.
	tools := []string{"read_file", "grep", "search_files", "glob"}
	for i := 0; i < driftThreshold; i++ {
		o.recordToolCall(tools[i%len(tools)], false, "/path.go")
	}
	msg := o.analyze(driftThreshold)
	if msg == "" {
		t.Fatal("expected drift intervention")
	}
	// Could be drift or stall — both are valid for all-readonly.
}

func TestOverseer_ProductiveActionResetsStall(t *testing.T) {
	o := newOverseerState()
	// Do some reads, then a productive action, then more reads.
	for i := 0; i < stallThreshold-5; i++ {
		o.recordToolCall("read_file", false, "/some/path.go")
	}
	o.recordToolCall("edit_file", false, "/some/path.go") // productive
	for i := 0; i < 5; i++ {
		o.recordToolCall("read_file", false, "/some/path.go")
	}
	// The stall check looks at the last stallThreshold entries.
	// After the edit, itersSinceProductive resets, and only 5 more reads.
	// But checkReadOnlyStall looks at trajectory, not itersSinceProductive.
	msg := o.analyze(stallThreshold + 1)
	// Should NOT trigger stall because the edit broke the read-only streak
	// in the last stallThreshold entries.
	if msg != "" && strings.Contains(msg, "reading and searching") {
		t.Fatalf("should not trigger stall after productive action, got: %s", msg)
	}
}

func TestOverseer_ErrorEscalation(t *testing.T) {
	o := newOverseerState()
	// First 10 calls: 1 error (10%)
	o.recordToolCall("run_command", true, "")
	for i := 0; i < 9; i++ {
		o.recordToolCall("run_command", false, "")
	}
	// Last 10 calls: 8 errors (80%)
	for i := 0; i < 2; i++ {
		o.recordToolCall("run_command", false, "")
	}
	for i := 0; i < 8; i++ {
		o.recordToolCall("run_command", true, "")
	}
	msg := o.analyze(20)
	if msg == "" {
		t.Fatal("expected error escalation intervention")
	}
	if !strings.Contains(msg, "error rate is increasing") {
		t.Fatalf("expected error escalation message, got: %s", msg)
	}
}

func TestOverseer_EachPatternFiresOnce(t *testing.T) {
	o := newOverseerState()
	// Trigger stall with mixed read-only tools.
	readOnlyTools := []string{"read_file", "grep", "search_files"}
	for i := 0; i < stallThreshold; i++ {
		o.recordToolCall(readOnlyTools[i%len(readOnlyTools)], false, "/path.go")
	}
	msg1 := o.analyze(stallThreshold)
	if msg1 == "" {
		t.Fatal("expected first intervention")
	}

	// Continue same pattern — should not re-trigger the same pattern type.
	// But a different pattern type could fire. We check that the SAME
	// message doesn't repeat.
	for i := 0; i < overseerInterval; i++ {
		o.recordToolCall(readOnlyTools[i%len(readOnlyTools)], false, "/path.go")
	}
	msg2 := o.analyze(stallThreshold + overseerInterval)
	// Either empty or a different intervention — not the same one.
	if msg2 == msg1 {
		t.Fatalf("expected no re-trigger of same pattern, got identical message: %s", msg2)
	}
}

func TestOverseer_Reset(t *testing.T) {
	o := newOverseerState()
	for i := 0; i < stallThreshold; i++ {
		o.recordToolCall("read_file", false, "/path.go")
	}
	o.analyze(stallThreshold)
	o.reset()

	if len(o.trajectory) != 0 {
		t.Fatal("trajectory should be empty after reset")
	}
	if len(o.fired) != 0 {
		t.Fatal("fired map should be empty after reset")
	}
	if o.itersSinceProductive != 0 {
		t.Fatal("itersSinceProductive should be 0 after reset")
	}
}

func TestOverseer_Cooldown(t *testing.T) {
	o := newOverseerState()
	// Fill trajectory with read-only calls to trigger intervention.
	readOnlyTools := []string{"read_file", "grep", "search_files"}
	for i := 0; i < stallThreshold; i++ {
		o.recordToolCall(readOnlyTools[i%len(readOnlyTools)], false, "/path.go")
	}
	msg1 := o.analyze(stallThreshold)
	if msg1 == "" {
		t.Fatal("expected intervention")
	}

	// Only 2 more iterations — cooldown should prevent re-analysis.
	for i := 0; i < 2; i++ {
		o.recordToolCall("read_file", false, "/path.go")
	}
	msg2 := o.analyze(stallThreshold + 2)
	if msg2 != "" {
		t.Fatalf("expected no intervention during cooldown, got: %s", msg2)
	}
}

func TestExtractFileHint(t *testing.T) {
	tests := []struct {
		name string
		args string
		want string
	}{
		{
			name: "path field",
			args: `{"path": "/src/main.go"}`,
			want: "/src/main.go",
		},
		{
			name: "file_path field",
			args: `{"file_path": "/src/main.go"}`,
			want: "/src/main.go",
		},
		{
			name: "no path",
			args: `{"pattern": "TODO"}`,
			want: "",
		},
		{
			name: "empty args",
			args: ``,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractFileHint("read_file", []byte(tt.args))
			if got != tt.want {
				t.Errorf("extractFileHint() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProductiveTools(t *testing.T) {
	productive := []string{"edit_file", "write_file", "run_command", "git_commit", "notebook_edit"}
	for _, tool := range productive {
		if !productiveTools[tool] {
			t.Errorf("%s should be productive", tool)
		}
	}

	nonProductive := []string{"read_file", "search_files", "grep", "glob", "list_directory"}
	for _, tool := range nonProductive {
		if productiveTools[tool] {
			t.Errorf("%s should not be productive", tool)
		}
	}
}

func TestExtractFileHintJSON(t *testing.T) {
	args, _ := json.Marshal(map[string]interface{}{
		"path": "/some/file.go",
		"edits": []map[string]string{
			{"old_text": "foo", "new_text": "bar"},
		},
	})
	got := extractFileHint("multi_edit_file", args)
	if got != "/some/file.go" {
		t.Errorf("extractFileHint with nested JSON = %q, want /some/file.go", got)
	}
}
