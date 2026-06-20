package tool

import (
	"testing"
)

// TestSubAgentBlockedTools verifies the blocklist contains all expected entries.
func TestSubAgentBlockedTools(t *testing.T) {
	expected := map[string]bool{
		"ask_user":          true,
		"spawn_agent":       true,
		"wait_agent":        true,
		"list_agents":       true,
		"teammate_spawn":    true,
		"teammate_shutdown": true,
		"team_create":       true,
		"team_delete":       true,
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

// TestSubAgentBlockedTools_RemovedFromClone verifies that applying the blocklist
// to a cloned registry actually removes all blocked tools, even when allowedTools
// explicitly requests them.
func TestSubAgentBlockedTools_RemovedFromClone(t *testing.T) {
	reg := NewRegistry()
	_ = RegisterBuiltinTools(reg, nil, "/tmp/test")

	// Simulate the BuildToolSet logic: unconditional removal
	cloned := reg.Clone()
	for _, name := range subAgentBlockedTools {
		cloned.Unregister(name)
	}

	// All blocked tools must be absent
	for _, blocked := range subAgentBlockedTools {
		if _, exists := cloned.tools[blocked]; exists {
			t.Errorf("blocked tool %q should not be in cloned registry", blocked)
		}
	}

	// Normal tools must still be present
	if _, exists := cloned.tools["read_file"]; !exists {
		t.Error("read_file should still be present after blocklist removal")
	}
	if _, exists := cloned.tools["write_file"]; !exists {
		t.Error("write_file should still be present after blocklist removal")
	}
}

// TestSubAgentBlockedTools_SurvivesAllowedFilter verifies that even when a
// hypothetical allowedTools filter includes blocked tool names, the blocked
// tools are already gone from the clone before the allowlist filter runs.
func TestSubAgentBlockedTools_SurvivesAllowedFilter(t *testing.T) {
	reg := NewRegistry()
	_ = RegisterBuiltinTools(reg, nil, "/tmp/test")

	// Step 1: unconditional block (runs first in BuildToolSet)
	cloned := reg.Clone()
	for _, name := range subAgentBlockedTools {
		cloned.Unregister(name)
	}

	// Step 2: allowlist filter (simulated — would normally keep only allowed tools)
	// Even if "ask_user" is in the allowed list, it's already gone
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

	// Blocked tools must still be absent
	for _, blocked := range subAgentBlockedTools {
		if _, exists := cloned.tools[blocked]; exists {
			t.Errorf("blocked tool %q survived allowlist filter — blocklist not applied first", blocked)
		}
	}
	// read_file (allowed, non-blocked) must survive
	if _, exists := cloned.tools["read_file"]; !exists {
		t.Error("read_file should survive both blocklist and allowlist filter")
	}
}

// TestSkillToolClonePreservesSystemPromptBuilder verifies that Clone() copies
// the SystemPromptBuilder field so cloned tool registries (used by teammates)
// keep the builder.
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
	// Verify it's the same function by calling it
	result := clonedSkill.SystemPromptBuilder("task", "Explore")
	if result != "test-prompt" {
		t.Errorf("cloned SystemPromptBuilder returned %q, expected %q", result, "test-prompt")
	}
}
