package tool

import (
	"testing"
)

func TestSubAgentBlockedTools(t *testing.T) {
	expected := map[string]bool{
		"ask_user":    true,
		"spawn_agent": true,

		"best_of_n": true,

		"compact_context":   true,
		"wait_agent":        true,
		"list_agents":       true,
		"cancel_agent":      true,
		"teammate_spawn":    true,
		"teammate_shutdown": true,
		"team_create":       true,
		"team_delete":       true,

		"swarm_task_create":   true,
		"swarm_task_claim":    true,
		"swarm_task_complete": true,
		"swarm_task_list":     true,
		"teammate_results":    true,
		"teammate_list":       true,
		"send_message":        true,
		"lanchat":             true,

		"workflow_run":  true,
		"a2a_remote":    true,
		"a2a_send_task": true,
	}
	for _, name := range subAgentBlockedTools {
		if !expected[name] {
			t.Errorf("unexpected entry in subAgentBlockedTools: %q", name)
		}
	}
	if len(subAgentBlockedTools) != len(expected) {
		t.Errorf("subAgentBlockedTools has %d entries, expected %d", len(subAgentBlockedTools), len(expected))
	}
}

func TestSubAgentBlockedTools_RemovedFromClone(t *testing.T) {
	reg := NewRegistry()
	_ = RegisterBuiltinTools(reg, nil, "/tmp/test", nil, nil)

	cloned := reg.Clone()
	for _, name := range subAgentBlockedTools {
		cloned.Unregister(name)
	}

	for _, blocked := range subAgentBlockedTools {
		if _, exists := cloned.tools[blocked]; exists {
			t.Errorf("blocked tool %q should not be in cloned registry", blocked)
		}
	}

	if _, exists := cloned.tools["read_file"]; !exists {
		t.Error("read_file should still be present after blocklist removal")
	}
	if _, exists := cloned.tools["write_file"]; !exists {
		t.Error("write_file should still be present after blocklist removal")
	}
}

func TestSubAgentBlockedTools_SurvivesAllowedFilter(t *testing.T) {
	reg := NewRegistry()
	_ = RegisterBuiltinTools(reg, nil, "/tmp/test", nil, nil)

	cloned := reg.Clone()
	for _, name := range subAgentBlockedTools {
		cloned.Unregister(name)
	}

	allowed := []string{"ask_user", "spawn_agent", "read_file"}
	allowedSet := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		allowedSet[a] = true
	}
	all := cloned.ToolNames()
	for _, name := range all {
		if !allowedSet[name] {
			cloned.Unregister(name)
		}
	}

	for _, blocked := range subAgentBlockedTools {
		if _, exists := cloned.tools[blocked]; exists {
			t.Errorf("blocked tool %q survived allowlist filter — blocklist not applied first", blocked)
		}
	}

	if _, exists := cloned.tools["read_file"]; !exists {
		t.Error("read_file should survive both blocklist and allowlist filter")
	}
}

func TestSkillToolClonePreservesSystemPromptBuilder(t *testing.T) {
	builder := func(task, agentType string) string { return "test-prompt" }
	st := SkillTool{
		SystemPromptBuilder: builder,
	}
	cloned := st.Clone()
	clonedSkill, ok := cloned.(SkillTool)
	if !ok {
		t.Fatalf("Clone() should return SkillTool, got %T", cloned)
	}
	if clonedSkill.SystemPromptBuilder == nil {
		t.Fatal("Clone() should preserve SystemPromptBuilder field")
	}

	result := clonedSkill.SystemPromptBuilder("task", "Explore")
	if result != "test-prompt" {
		t.Errorf("cloned SystemPromptBuilder returned %q, expected %q", result, "test-prompt")
	}
}
