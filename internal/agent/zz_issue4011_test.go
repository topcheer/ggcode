package agent

import (
	"fmt"
	"testing"
)

// #4011 probes: spawn_agent verify-delegation must register in the #1153
// registry so the later task_output attributes the outcome (GREEN resets,
// FAILED marks), and non-verify spawns stay unregistered.

func zz4011State() *prematureSuccessState {
	return newPrematureSuccessState()
}

func TestIssue4011_SpawnVerifyRegistered(t *testing.T) {
	p := zz4011State()
	p.recordToolCall("edit_file", map[string]interface{}{"file_path": "a.go"}, false, "ok")
	p.recordToolCall("spawn_agent",
		map[string]interface{}{"task": "run go test ./... and report results"},
		false,
		"Sub-agent spawned with ID: agent-42\nUse wait_agent or list_agents to monitor progress.")
	if !p.psJobIsVerify("agent-42") {
		t.Fatal("verify-type spawn must register its agent_id in the #1153 registry (#4011)")
	}
}

func TestIssue4011_TaskOutputAttributesOutcome(t *testing.T) {
	// GREEN: verify + editsSinceVerify reset.
	p := zz4011State()
	p.recordToolCall("edit_file", map[string]interface{}{"file_path": "a.go"}, false, "ok")
	p.recordToolCall("spawn_agent",
		map[string]interface{}{"task": "go build ./... verification"},
		false, "Sub-agent spawned with ID: agent-g")
	p.recordToolCall("task_output",
		map[string]interface{}{"task_id": "agent-g"},
		false, "all tests passed (sub-agent final output, no Status header)")
	if p.editsSinceVerify != 0 || p.lastVerifyFailed {
		t.Fatalf("GREEN sub-agent verify must reset state: edits=%d failed=%v", p.editsSinceVerify, p.lastVerifyFailed)
	}

	// FAILED: lastVerifyFailed armed so prematureSuccess/correctionSpiral see it.
	p2 := zz4011State()
	p2.recordToolCall("edit_file", map[string]interface{}{"file_path": "a.go"}, false, "ok")
	p2.recordToolCall("spawn_agent",
		map[string]interface{}{"task": "go test ./... verification"},
		false, "Sub-agent spawned with ID: agent-f")
	p2.recordToolCall("task_output",
		map[string]interface{}{"task_id": "agent-f"},
		true, fmt.Sprintf("TestIssueX failed (sub-agent output)"))
	if !p2.lastVerifyFailed {
		t.Fatal("FAILED sub-agent verify must arm lastVerifyFailed (#4011)")
	}
	// Consumed once: a second poll must not re-grade.
	p2.recordToolCall("task_output",
		map[string]interface{}{"task_id": "agent-f"},
		true, "TestIssueX failed")
	if p2.psJobIsVerify("agent-f") {
		t.Fatal("terminal task_output must consume the registry entry once")
	}
}

func TestIssue4011_NonVerifySpawnNotRegistered(t *testing.T) {
	p := zz4011State()
	p.recordToolCall("spawn_agent",
		map[string]interface{}{"task": "research the docs and summarize"},
		false, "Sub-agent spawned with ID: agent-nv")
	if p.psJobIsVerify("agent-nv") {
		t.Fatal("non-verify spawn must not register as a verify job")
	}
}
