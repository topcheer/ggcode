package agent

import (
	"testing"
)

// TestIssue4011_SpawnAgentRegistersTaskForAttribution verifies the 4011 fix:
// a successful spawn_agent registers its sub-agent ID (keyed by the same
// task_id task_output later queries) so verify outcomes become visible to
// prematureSuccess attribution. Before the fix the registry's only writer was
// the start_command path, so the task_output lookup never hit and sub-agent
// verify results were invisible (the 1153 attribution contract).
func TestIssue4011_SpawnAgentRegistersTaskForAttribution(t *testing.T) {
	p := newPrematureSuccessState()

	// Agent edits code, then delegates a verify run to a sub-agent.
	p.recordToolCall("edit_file", map[string]interface{}{"file_path": "/w/a.go"}, false, "ok")
	if p.editsSinceVerify != 1 {
		t.Fatalf("editsSinceVerify = %d, want 1", p.editsSinceVerify)
	}

	spawnResult := "Sub-agent spawned with ID: agent-7654321\nUse wait_agent or list_agents to monitor progress and retrieve the result."
	p.recordToolCall("spawn_agent", map[string]interface{}{
		"task": "run go test ./internal/agent/ and report pass/fail",
	}, false, spawnResult)

	// The task_id namespace must now be registered as a verify job.
	if !p.psJobIsVerify("agent-7654321") {
		t.Fatalf("spawned sub-agent task not registered as verify job - task_output attribution stays dead (4011)")
	}

	// Sub-agent FAILED its verify run: task_output result carries the failure.
	p.recordToolCall("task_output", map[string]interface{}{
		"task_id": "agent-7654321",
	}, false, "FAIL\n--- FAIL: TestX")

	if !p.lastVerifyFailed {
		t.Fatalf("sub-agent verify failure not recorded: lastVerifyFailed=false, want true")
	}

	// Green path: a passing sub-agent verify resets the unverified-edits streak.
	p.recordToolCall("edit_file", map[string]interface{}{"file_path": "/w/b.go"}, false, "ok")
	p.recordToolCall("spawn_agent", map[string]interface{}{
		"task": "run go test ./... and report pass/fail",
	}, false, "Sub-agent spawned with ID: agent-9999\nUse wait_agent or list_agents.")
	p.recordToolCall("task_output", map[string]interface{}{
		"task_id": "agent-9999",
	}, false, "ok - all tests passed")

	if p.lastVerifyFailed {
		t.Fatalf("green sub-agent verify did not clear lastVerifyFailed")
	}
	if p.editsSinceVerify != 0 {
		t.Fatalf("green sub-agent verify did not reset editsSinceVerify: got %d, want 0", p.editsSinceVerify)
	}
}

// TestIssue4011_NonVerifySpawnNotAttributed: a sub-agent spawned for
// non-verify work must NOT mark edits as verified (conservative attribution).
func TestIssue4011_NonVerifySpawnNotAttributed(t *testing.T) {
	p := newPrematureSuccessState()
	p.recordToolCall("edit_file", map[string]interface{}{"file_path": "/w/a.go"}, false, "ok")

	p.recordToolCall("spawn_agent", map[string]interface{}{
		"task": "write marketing copy for the landing page",
	}, false, "Sub-agent spawned with ID: agent-copy1\nUse wait_agent or list_agents.")

	if p.psJobIsVerify("agent-copy1") {
		t.Fatalf("non-verify spawn misclassified as verify job")
	}
	// Its task_output result must be ignored (no attribution).
	p.recordToolCall("task_output", map[string]interface{}{
		"task_id": "agent-copy1",
	}, false, "copy done")

	if p.editsSinceVerify != 1 {
		t.Fatalf("non-verify task_output must not touch attribution: editsSinceVerify=%d, want 1", p.editsSinceVerify)
	}
}

// TestIssue4011_SpawnErrorIgnored: a failed spawn registers nothing.
func TestIssue4011_SpawnErrorIgnored(t *testing.T) {
	p := newPrematureSuccessState()
	p.recordToolCall("spawn_agent", map[string]interface{}{"task": "x"}, true, "sub-agent manager not available")
	if len(p.backgroundJobs) != 0 {
		t.Fatalf("failed spawn must not register: got %d jobs", len(p.backgroundJobs))
	}
}

// TestIssue4011_ExtractorContract pins the spawn-result header contract
// against accidental wording drift in spawn_agent.go.
func TestIssue4011_ExtractorContract(t *testing.T) {
	if got := psExtractSpawnedAgentID("Sub-agent spawned with ID: agent-42\nUse wait_agent or list_agents to monitor progress and retrieve the result."); got != "agent-42" {
		t.Fatalf("extractor got %q, want agent-42", got)
	}
	if got := psExtractSpawnedAgentID("no header here"); got != "" {
		t.Fatalf("extractor should return empty without header, got %q", got)
	}
}
