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
	// Push trajectory past drift threshold so driftLevel advances.
	for i := 0; i < driftThreshold*3; i++ {
		o.recordToolCall("read_file", false, "/path.go")
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

	// Verify drift detection works again after reset.
	for i := 0; i < driftThreshold; i++ {
		o.recordToolCall("read_file", false, "/path.go")
	}
	msg := o.checkDrift(o.trajectory)
	if msg == "" {
		t.Fatal("drift detection should fire after reset — driftLevel was not reset")
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

// TestOverseer_FailedCommandNotProductive verifies that a failed run_command
// does NOT reset itersSinceProductive. Previously, ALL run_command calls were
// treated as productive, which suppressed drift detection when the agent was
// stuck running failing builds in a loop.
func TestOverseer_FailedCommandNotProductive(t *testing.T) {
	o := newOverseerState()

	// Run driftThreshold failed commands — these should NOT reset the drift counter.
	for i := 0; i < driftThreshold; i++ {
		o.recordToolCall("run_command", true, "") // failed build
	}

	if o.itersSinceProductive < driftThreshold {
		t.Fatalf("expected itersSinceProductive >= %d after %d failed commands, got %d",
			driftThreshold, driftThreshold, o.itersSinceProductive)
	}

	// Drift should fire because failed commands are not productive.
	msg := o.analyze(driftThreshold)
	if msg == "" {
		t.Fatal("expected drift intervention after repeated failed commands")
	}
}

// TestOverseer_SuccessfulCommandResetsProductive verifies that a successful
// run_command DOES reset itersSinceProductive (the normal case).
func TestOverseer_SuccessfulCommandResetsProductive(t *testing.T) {
	o := newOverseerState()

	// Some read-only calls.
	for i := 0; i < 10; i++ {
		o.recordToolCall("read_file", false, "/path.go")
	}
	if o.itersSinceProductive != 10 {
		t.Fatalf("expected itersSinceProductive=10, got %d", o.itersSinceProductive)
	}

	// A successful command resets the counter.
	o.recordToolCall("run_command", false, "")
	if o.itersSinceProductive != 0 {
		t.Fatalf("expected itersSinceProductive=0 after successful command, got %d", o.itersSinceProductive)
	}

	// File-read tracking should also be reset.
	if len(o.fileReadsSinceEdit) != 0 {
		t.Fatalf("expected fileReadsSinceEdit cleared after successful command, got %d entries", len(o.fileReadsSinceEdit))
	}
}

func TestOverseer_ProgressiveDrift(t *testing.T) {
	o := newOverseerState()
	tools := []string{"read_file", "grep", "search_files", "glob"}

	// Level 1: driftThreshold iterations
	for i := 0; i < driftThreshold; i++ {
		o.recordToolCall(tools[i%len(tools)], false, "/path.go")
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

	// Level 2: 2×driftThreshold iterations
	for i := 0; i < driftThreshold; i++ {
		o.recordToolCall(tools[i%len(tools)], false, "/path.go")
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

	// Level 3: 3×driftThreshold iterations
	for i := 0; i < driftThreshold; i++ {
		o.recordToolCall(tools[i%len(tools)], false, "/path.go")
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

	// No further escalation beyond level 3
	for i := 0; i < driftThreshold; i++ {
		o.recordToolCall(tools[i%len(tools)], false, "/path.go")
	}
	msg4 := o.checkDrift(o.trajectory)
	if msg4 != "" {
		t.Fatalf("expected no further drift escalation beyond level 3, got: %s", msg4)
	}
}

func TestOverseer_DriftResetsOnProductiveAction(t *testing.T) {
	o := newOverseerState()
	tools := []string{"read_file", "grep", "search_files", "glob"}

	// Trigger drift level 1
	for i := 0; i < driftThreshold; i++ {
		o.recordToolCall(tools[i%len(tools)], false, "/path.go")
	}
	msg := o.checkDrift(o.trajectory)
	if msg == "" {
		t.Fatal("expected drift intervention")
	}

	// Productive action resets drift tracking (including driftLevel)
	o.recordToolCall("edit_file", false, "/path.go")
	if o.driftLevel != 0 {
		t.Fatalf("expected driftLevel reset to 0 after productive action, got %d", o.driftLevel)
	}

	// More read-only calls — should trigger level 1 again, not level 2
	for i := 0; i < driftThreshold; i++ {
		o.recordToolCall(tools[i%len(tools)], false, "/path.go")
	}
	msg2 := o.checkDrift(o.trajectory)
	if msg2 == "" {
		t.Fatal("expected drift level 1 again after productive action")
	}
	if !strings.Contains(msg2, "Re-anchor") {
		t.Fatalf("expected level 1 guidance after reset, got: %s", msg2)
	}
}

// TestOverseer_ResearchMode_NoPrematureStall verifies that in research mode,
// read-only operations don't trigger stall/spam/drift at normal thresholds.
func TestOverseer_ResearchMode_NoPrematureStall(t *testing.T) {
	o := newOverseerState()
	o.researchMode = true

	// In normal mode, 15 mixed read-only calls would trigger stall.
	// In research mode, this should NOT fire.
	readOnlyTools := []string{"read_file", "search_files", "grep", "list_directory", "glob",
		"git_log", "git_status", "git_diff", "lsp_definition", "lsp_references",
		"web_search", "web_fetch", "lsp_symbols", "git_blame", "git_show"}
	for i := 0; i < stallThreshold; i++ {
		o.recordToolCall(readOnlyTools[i%len(readOnlyTools)], false, "/path.go")
	}
	msg := o.analyze(stallThreshold)
	if msg != "" {
		t.Fatalf("research mode should not trigger stall at %d iterations, got: %s", stallThreshold, msg)
	}
}

// TestOverseer_ResearchMode_NoPrematureSpam verifies that in research mode,
// repeated read tool calls don't trigger spam at the normal threshold.
func TestOverseer_ResearchMode_NoPrematureSpam(t *testing.T) {
	o := newOverseerState()
	o.researchMode = true

	// In normal mode, >6 calls to search_files triggers spam.
	// In research mode, we need >researchSpamThreshold (15).
	for i := 0; i < spamThreshold+5; i++ { // 11 calls — above normal threshold
		o.recordToolCall("search_files", false, "")
	}
	msg := o.analyze(spamThreshold + 5)
	if msg != "" {
		t.Fatalf("research mode should not trigger spam at %d calls (threshold=%d), got: %s",
			spamThreshold+5, researchSpamThreshold, msg)
	}
}

// TestOverseer_ResearchMode_NoPrematureDrift verifies that in research mode,
// drift doesn't fire at the normal threshold.
func TestOverseer_ResearchMode_NoPrematureDrift(t *testing.T) {
	o := newOverseerState()
	o.researchMode = true

	tools := []string{"read_file", "grep", "search_files", "glob"}
	// In normal mode, driftThreshold (20) iterations triggers drift.
	// In research mode, this should NOT fire.
	for i := 0; i < driftThreshold; i++ {
		o.recordToolCall(tools[i%len(tools)], false, "/path.go")
	}
	msg := o.analyze(driftThreshold)
	if msg != "" {
		t.Fatalf("research mode should not trigger drift at %d iterations, got: %s", driftThreshold, msg)
	}
}

// TestOverseer_ResearchMode_StallAtHighThreshold verifies that stall DOES
// eventually fire in research mode at the higher threshold.
func TestOverseer_ResearchMode_StallAtHighThreshold(t *testing.T) {
	o := newOverseerState()
	o.researchMode = true

	readOnlyTools := []string{"read_file", "search_files", "grep", "list_directory", "glob"}
	// Need researchStallThreshold (40) consecutive read-only calls.
	for i := 0; i < researchStallThreshold; i++ {
		o.recordToolCall(readOnlyTools[i%len(readOnlyTools)], false, "/path.go")
	}
	msg := o.analyze(researchStallThreshold)
	if msg == "" {
		t.Fatal("expected stall intervention at research threshold")
	}
	// Research-mode stall message should mention "research" or "findings", not "implementing your solution"
	if strings.Contains(msg, "implementing your solution") {
		t.Fatalf("research-mode stall should not push for implementation, got: %s", msg)
	}
}

// TestOverseer_ResearchMode_ResearchToolsAreProductive verifies that in
// research mode, web_search/code_search count as productive work and reset
// the drift counter.
func TestOverseer_ResearchMode_ResearchToolsAreProductive(t *testing.T) {
	o := newOverseerState()
	o.researchMode = true

	// Read some files, then do a web_search (should reset productive counter).
	for i := 0; i < 10; i++ {
		o.recordToolCall("read_file", false, "/path.go")
	}
	o.recordToolCall("web_search", false, "")
	if o.itersSinceProductive != 0 {
		t.Fatalf("expected itersSinceProductive=0 after web_search in research mode, got %d", o.itersSinceProductive)
	}

	// code_search should also be productive in research mode.
	for i := 0; i < 10; i++ {
		o.recordToolCall("read_file", false, "/path.go")
	}
	o.recordToolCall("code_search", false, "")
	if o.itersSinceProductive != 0 {
		t.Fatalf("expected itersSinceProductive=0 after code_search in research mode, got %d", o.itersSinceProductive)
	}
}

// TestOverseer_ResearchMode_ResearchToolsNotProductiveInNormalMode verifies
// that web_search/code_search are NOT productive in normal (implementation) mode.
func TestOverseer_ResearchMode_ResearchToolsNotProductiveInNormalMode(t *testing.T) {
	o := newOverseerState()
	o.researchMode = false // normal implementation mode

	for i := 0; i < 10; i++ {
		o.recordToolCall("read_file", false, "/path.go")
	}
	o.recordToolCall("web_search", false, "")
	if o.itersSinceProductive == 0 {
		t.Fatal("web_search should NOT reset productive counter in normal mode")
	}
}

// TestDetectResearchMode verifies keyword detection for research tasks.
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
