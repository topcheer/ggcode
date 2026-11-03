package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOverseer_TooFewCalls(t *testing.T) {
	o := newOverseerState()
	for i := 0; i < overseerInterval-1; i++ {
		o.recordToolCall("read_file", false, "/some/path.go", "", i)
	}
	msg := o.analyze(overseerInterval - 1)
	if msg != "" {
		t.Fatalf("expected no intervention with < overseerInterval calls, got: %s", msg)
	}
}

func TestOverseer_ToolSpam(t *testing.T) {
	o := newOverseerState()

	for i := 0; i < overseerInterval; i++ {
		o.recordToolCall("search_files", false, "", "", i)
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

	readOnlyTools := []string{"read_file", "search_files", "grep", "list_directory", "glob",
		"git_log", "git_status", "git_diff", "lsp_definition", "lsp_references",
		"web_search", "web_fetch", "lsp_symbols", "git_blame", "git_show"}
	for i := 0; i < stallThreshold; i++ {
		o.recordToolCall(readOnlyTools[i%len(readOnlyTools)], false, "/path.go", "", i)
	}
	msg := o.analyze(stallThreshold)
	if msg == "" {
		t.Fatal("expected read-only stall intervention")
	}

}

func TestOverseer_FileStuck(t *testing.T) {
	o := newOverseerState()

	for i := 0; i < fileStuckThreshold; i++ {
		o.recordToolCall("read_file", false, "/important/file.go", "", i)
	}

	for i := 0; i < overseerInterval-fileStuckThreshold; i++ {
		o.recordToolCall("grep", false, "", "", i)
	}
	msg := o.analyze(overseerInterval)
	if msg == "" {
		t.Fatal("expected intervention (file stuck or tool spam)")
	}
}

func TestOverseer_Drift(t *testing.T) {
	o := newOverseerState()

	tools := []string{"read_file", "grep", "search_files", "glob"}
	for i := 0; i < driftThreshold; i++ {
		o.recordToolCall(tools[i%len(tools)], false, "/path.go", "", i)
	}
	msg := o.analyze(driftThreshold)
	if msg == "" {
		t.Fatal("expected drift intervention")
	}

}

func TestOverseer_ProductiveActionResetsStall(t *testing.T) {
	o := newOverseerState()

	for i := 0; i < stallThreshold-5; i++ {
		o.recordToolCall("read_file", false, "/some/path.go", "", i)
	}
	o.recordToolCall("edit_file", false, "/some/path.go", "", 0)
	for i := 0; i < 5; i++ {
		o.recordToolCall("read_file", false, "/some/path.go", "", i)
	}

	msg := o.analyze(stallThreshold + 1)

	if msg != "" && strings.Contains(msg, "reading and searching") {
		t.Fatalf("should not trigger stall after productive action, got: %s", msg)
	}
}

func TestOverseer_ErrorEscalation(t *testing.T) {
	o := newOverseerState()

	o.recordToolCall("run_command", true, "", "", 0)
	for i := 0; i < 9; i++ {
		o.recordToolCall("run_command", false, "", "", i)
	}

	for i := 0; i < 2; i++ {
		o.recordToolCall("run_command", false, "", "", i)
	}
	for i := 0; i < 8; i++ {
		o.recordToolCall("run_command", true, "", "", 0)
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

	readOnlyTools := []string{"read_file", "grep", "search_files"}
	for i := 0; i < stallThreshold; i++ {
		o.recordToolCall(readOnlyTools[i%len(readOnlyTools)], false, "/path.go", "", i)
	}
	msg1 := o.analyze(stallThreshold)
	if msg1 == "" {
		t.Fatal("expected first intervention")
	}

	for i := 0; i < overseerInterval; i++ {
		o.recordToolCall(readOnlyTools[i%len(readOnlyTools)], false, "/path.go", "", i)
	}
	msg2 := o.analyze(stallThreshold + overseerInterval)

	if msg2 == msg1 {
		t.Fatalf("expected no re-trigger of same pattern, got identical message: %s", msg2)
	}
}

func TestOverseer_Reset(t *testing.T) {
	o := newOverseerState()

	for i := 0; i < driftThreshold*3; i++ {
		o.recordToolCall("read_file", false, "/path.go", "", i)
	}
	o.checkDrift(o.trajectory)
	if o.driftLevel == 0 {
		t.Fatal("expected driftLevel > 0 before reset")
	}
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
	if o.driftLevel != 0 {
		t.Fatalf("driftLevel should be 0 after reset, got %d", o.driftLevel)
	}

	for i := 0; i < driftThreshold; i++ {
		o.recordToolCall("read_file", false, "/path.go", "", i)
	}
	msg := o.checkDrift(o.trajectory)
	if msg == "" {
		t.Fatal("drift detection should fire after reset — driftLevel was not reset")
	}
}

func TestOverseer_Cooldown(t *testing.T) {
	o := newOverseerState()

	readOnlyTools := []string{"read_file", "grep", "search_files"}
	for i := 0; i < stallThreshold; i++ {
		o.recordToolCall(readOnlyTools[i%len(readOnlyTools)], false, "/path.go", "", i)
	}
	msg1 := o.analyze(stallThreshold)
	if msg1 == "" {
		t.Fatal("expected intervention")
	}

	for i := 0; i < 2; i++ {
		o.recordToolCall("read_file", false, "/path.go", "", i)
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

func TestExtractFileHints(t *testing.T) {

	batch := []byte(`{"files":[{"path":"/a.go","offset":1,"limit":10},{"path":"/b.go"},{"path":"/c.go"},{"path":"/d.go"}]}`)
	got := extractFileHints("multi_file_read", batch)
	want := []string{"/a.go", "/b.go", "/c.go", "/d.go"}
	if len(got) != len(want) {
		t.Fatalf("batch: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("batch[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	single := extractFileHints("read_file", []byte(`{"path":"/x.go","offset":5}`))
	if len(single) != 1 || single[0] != "/x.go" {
		t.Errorf("single: got %v, want [/x.go]", single)
	}

	if got := extractFileHints("multi_file_read", []byte(`{"files": not-json`)); got != nil {
		t.Errorf("malformed: got %v, want nil", got)
	}

	if got := extractFileHints("multi_file_read", []byte(`{"files":[]}`)); got != nil {
		t.Errorf("empty batch: got %v, want nil", got)
	}
}

func TestOverseer_FailedCommandNotProductive(t *testing.T) {
	o := newOverseerState()

	for i := 0; i < driftThreshold; i++ {
		o.recordToolCall("run_command", true, "", "", i)
	}

	if o.itersSinceProductive < driftThreshold {
		t.Fatalf("expected itersSinceProductive >= %d after %d failed commands, got %d",
			driftThreshold, driftThreshold, o.itersSinceProductive)
	}

	msg := o.analyze(driftThreshold)
	if msg == "" {
		t.Fatal("expected drift intervention after repeated failed commands")
	}
}

func TestOverseer_SuccessfulCommandResetsProductive(t *testing.T) {
	o := newOverseerState()

	for i := 0; i < 10; i++ {
		o.recordToolCall("read_file", false, "/path.go", "", i)
	}
	if o.itersSinceProductive != 10 {
		t.Fatalf("expected itersSinceProductive=10, got %d", o.itersSinceProductive)
	}

	o.recordToolCall("run_command", false, "", "", 0)
	if o.itersSinceProductive != 0 {
		t.Fatalf("expected itersSinceProductive=0 after successful command, got %d", o.itersSinceProductive)
	}

	if len(o.fileReadsSinceEdit) != 0 {
		t.Fatalf("expected fileReadsSinceEdit cleared after successful command, got %d entries", len(o.fileReadsSinceEdit))
	}
}

func TestOverseer_ProgressiveDrift(t *testing.T) {
	o := newOverseerState()
	tools := []string{"read_file", "grep", "search_files", "glob"}

	for i := 0; i < driftThreshold; i++ {
		o.recordToolCall(tools[i%len(tools)], false, "/path.go", "", i)
	}
	msg1 := o.checkDrift(o.trajectory)
	if msg1 == "" {
		t.Fatal("expected drift level 1 intervention")
	}
	if !strings.Contains(msg1, "Re-anchor") {
		t.Fatalf("level 1 should mention re-anchoring, got: %s", msg1)
	}
	if o.driftLevel != 1 {
		t.Fatalf("expected driftLevel=1, got %d", o.driftLevel)
	}

	for i := 0; i < driftThreshold; i++ {
		o.recordToolCall(tools[i%len(tools)], false, "/path.go", "", i)
	}
	msg2 := o.checkDrift(o.trajectory)
	if msg2 == "" {
		t.Fatal("expected drift level 2 intervention")
	}
	if !strings.Contains(msg2, "significant stall") {
		t.Fatalf("level 2 should mention significant stall, got: %s", msg2)
	}
	if o.driftLevel != 2 {
		t.Fatalf("expected driftLevel=2, got %d", o.driftLevel)
	}

	for i := 0; i < driftThreshold; i++ {
		o.recordToolCall(tools[i%len(tools)], false, "/path.go", "", i)
	}
	msg3 := o.checkDrift(o.trajectory)
	if msg3 == "" {
		t.Fatal("expected drift level 3 intervention")
	}
	if !strings.Contains(msg3, "critical stall") {
		t.Fatalf("level 3 should mention critical stall, got: %s", msg3)
	}
	if o.driftLevel != 3 {
		t.Fatalf("expected driftLevel=3, got %d", o.driftLevel)
	}

	for i := 0; i < driftThreshold; i++ {
		o.recordToolCall(tools[i%len(tools)], false, "/path.go", "", i)
	}
	msg4 := o.checkDrift(o.trajectory)
	if msg4 != "" {
		t.Fatalf("expected no further drift escalation beyond level 3, got: %s", msg4)
	}
}

func TestOverseer_DriftResetsOnProductiveAction(t *testing.T) {
	o := newOverseerState()
	tools := []string{"read_file", "grep", "search_files", "glob"}

	for i := 0; i < driftThreshold; i++ {
		o.recordToolCall(tools[i%len(tools)], false, "/path.go", "", i)
	}
	msg := o.checkDrift(o.trajectory)
	if msg == "" {
		t.Fatal("expected drift intervention")
	}

	o.recordToolCall("edit_file", false, "/path.go", "", 0)
	if o.driftLevel != 0 {
		t.Fatalf("expected driftLevel reset to 0 after productive action, got %d", o.driftLevel)
	}

	for i := 0; i < driftThreshold; i++ {
		o.recordToolCall(tools[i%len(tools)], false, "/path.go", "", i+driftThreshold)
	}
	msg2 := o.checkDrift(o.trajectory)
	if msg2 == "" {
		t.Fatal("expected drift level 1 again after productive action")
	}
	if !strings.Contains(msg2, "Re-anchor") {
		t.Fatalf("expected level 1 guidance after reset, got: %s", msg2)
	}
}

func TestOverseer_ResearchMode_NoPrematureStall(t *testing.T) {
	o := newOverseerState()
	o.researchMode = true

	readOnlyTools := []string{"read_file", "search_files", "grep", "list_directory", "glob",
		"git_log", "git_status", "git_diff", "lsp_definition", "lsp_references",
		"web_search", "web_fetch", "lsp_symbols", "git_blame", "git_show"}
	for i := 0; i < stallThreshold; i++ {
		o.recordToolCall(readOnlyTools[i%len(readOnlyTools)], false, "/path.go", "", i)
	}
	msg := o.analyze(stallThreshold)
	if msg != "" {
		t.Fatalf("research mode should not trigger stall at %d iterations, got: %s", stallThreshold, msg)
	}
}

func TestOverseer_ResearchMode_NoPrematureSpam(t *testing.T) {
	o := newOverseerState()
	o.researchMode = true

	for i := 0; i < spamThreshold+5; i++ {
		o.recordToolCall("search_files", false, "", "", i)
	}
	msg := o.analyze(spamThreshold + 5)
	if msg != "" {
		t.Fatalf("research mode should not trigger spam at %d calls (threshold=%d), got: %s",
			spamThreshold+5, researchSpamThreshold, msg)
	}
}

func TestOverseer_ResearchMode_NoPrematureDrift(t *testing.T) {
	o := newOverseerState()
	o.researchMode = true

	tools := []string{"read_file", "grep", "search_files", "glob"}

	for i := 0; i < driftThreshold; i++ {
		o.recordToolCall(tools[i%len(tools)], false, "/path.go", "", i)
	}
	msg := o.analyze(driftThreshold)
	if msg != "" {
		t.Fatalf("research mode should not trigger drift at %d iterations, got: %s", driftThreshold, msg)
	}
}

func TestOverseer_ResearchMode_StallAtHighThreshold(t *testing.T) {
	o := newOverseerState()
	o.researchMode = true

	readOnlyTools := []string{"read_file", "search_files", "grep", "list_directory", "glob"}

	for i := 0; i < researchStallThreshold; i++ {
		o.recordToolCall(readOnlyTools[i%len(readOnlyTools)], false, "/path.go", "", i)
	}
	msg := o.analyze(researchStallThreshold)
	if msg == "" {
		t.Fatal("expected stall intervention at research threshold")
	}

	if strings.Contains(msg, "implementing your solution") {
		t.Fatalf("research-mode stall should not push for implementation, got: %s", msg)
	}
}

func TestOverseer_ResearchMode_ResearchToolsAreProductive(t *testing.T) {
	o := newOverseerState()
	o.researchMode = true

	for i := 0; i < 10; i++ {
		o.recordToolCall("read_file", false, "/path.go", "", i)
	}
	o.recordToolCall("web_search", false, "", "", 0)
	if o.itersSinceProductive != 0 {
		t.Fatalf("expected itersSinceProductive=0 after web_search in research mode, got %d", o.itersSinceProductive)
	}

	for i := 0; i < 10; i++ {
		o.recordToolCall("read_file", false, "/path.go", "", i)
	}
	o.recordToolCall("code_search", false, "", "", 999)
	if o.itersSinceProductive != 0 {
		t.Fatalf("expected itersSinceProductive=0 after code_search in research mode, got %d", o.itersSinceProductive)
	}
}

func TestOverseer_ResearchMode_ResearchToolsNotProductiveInNormalMode(t *testing.T) {
	o := newOverseerState()
	o.researchMode = false

	for i := 0; i < 10; i++ {
		o.recordToolCall("read_file", false, "/path.go", "", i)
	}
	o.recordToolCall("web_search", false, "", "", 999)
	if o.itersSinceProductive == 0 {
		t.Fatal("web_search should NOT reset productive counter in normal mode")
	}
}

func TestDetectResearchMode(t *testing.T) {
	researchTasks := []string{
		"Research the latest trends in AI agents",
		"Analyze the codebase for security gaps",
		"Audit the authentication flow",
		"Investigate memory leak in goroutines",
		"Explore competitor implementations",
		"Evaluate different caching strategies",
		"Assess the impact of the refactoring",
		"Compare performance before and after optimization",
		"Benchmark the new parser against the old one",
		"Review the API for consistency",
	}
	for _, task := range researchTasks {
		if !detectResearchMode(task) {
			t.Errorf("detectResearchMode(%q) = false, want true", task)
		}
	}

	implTasks := []string{
		"Fix the bug in the login handler",
		"Add a new endpoint for user profiles",
		"Build the dashboard component",
		"Deploy the service to production",
		"Refactor the database connection pool",
	}
	for _, task := range implTasks {
		if detectResearchMode(task) {
			t.Errorf("detectResearchMode(%q) = true, want false", task)
		}
	}
}

func TestReadOnlyTablesIncludeFormerGaps(t *testing.T) {

	for _, name := range []string{
		"code_search", "git_stash_list", "lsp_workspace_symbols",
		"lsp_implementation", "lsp_code_actions", "dep_graph",
		"scan_todos", "code_health",
	} {
		if !readOnlyTools[name] {
			t.Errorf("readOnlyTools missing pure-read tool %q (stall nudge suppressed)", name)
		}
		if !effortReadOnlyTools[name] {
			t.Errorf("effortReadOnlyTools missing pure-read tool %q", name)
		}
	}
	if !effortReadOnlyTools["screenshot"] {
		t.Error("effortReadOnlyTools missing screenshot (capture-only)")
	}
	if toolCategoryMap["screenshot"] != thermalExplore {
		t.Errorf("screenshot must be thermalExplore, got %v", toolCategoryMap["screenshot"])
	}
}

func TestExtractWrittenPaths(t *testing.T) {
	got := extractWrittenPaths(`{"written_files":1,"written_paths":["/a.go"],"failed_files":1,"failed_paths":["/b.go"]}`)
	if len(got) != 1 || got[0] != "/a.go" {
		t.Fatalf("expected [/a.go], got %v", got)
	}
	if extractWrittenPaths("no json here") != nil {
		t.Fatal("non-JSON body must return nil")
	}
	if extractWrittenPaths(`{"ok":true}`) != nil {
		t.Fatal("body without written_paths must return nil")
	}
}
