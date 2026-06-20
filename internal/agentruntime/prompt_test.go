package agentruntime

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/tool"
)

func TestBuildSubAgentSystemPrompt_FullContext(t *testing.T) {
	// Build a minimal registry with a few tools
	reg := tool.NewRegistry()
	_ = tool.RegisterBuiltinTools(reg, nil, "/tmp/test-project")

	cfg := &config.Config{
		Language: "en",
	}
	ctx := SubAgentPromptContext{
		Cfg:              cfg,
		WorkingDir:       "/tmp/test-project",
		Registry:         reg,
		GitStatus:        func() string { return "## main\nM file.go" },
		RemoteAgentsInfo: func() string { return "" },
	}

	prompt := BuildSubAgentSystemPrompt(ctx, "review the code", "Explore")

	// Verify the prompt contains expected sections
	checks := []struct {
		name     string
		contains string
	}{
		{"working dir", "/tmp/test-project"},
		{"task", "review the code"},
		{"sub-agent constraint: bypass", "Permission mode is bypass"},
		{"sub-agent constraint: no ask_user", "Do not use `ask_user`"},
		{"sub-agent constraint: no spawn", "Do not spawn further sub-agents"},
		{"VS16 emoji", "Variation Selector-16"},
		{"tool names present", "read_file"},
		{"git status", "## main"},
	}

	for _, c := range checks {
		if !strings.Contains(prompt, c.contains) {
			t.Errorf("%s: prompt missing expected text %q", c.name, c.contains)
		}
	}
}

func TestBuildSubAgentSystemPrompt_NilFields(t *testing.T) {
	cfg := &config.Config{Language: "en"}
	ctx := SubAgentPromptContext{
		Cfg:        cfg,
		WorkingDir: "/tmp/test",
		// Registry, CommandMgr, GlobalAutoMem, ProjectAutoMem, GitStatus, RemoteAgentsInfo all nil
	}

	prompt := BuildSubAgentSystemPrompt(ctx, "do something", "")

	// Should still produce a prompt with the base system prompt and constraints
	if !strings.Contains(prompt, "Permission mode is bypass") {
		t.Error("prompt should contain bypass constraint even with nil fields")
	}
	if !strings.Contains(prompt, "do something") {
		t.Error("prompt should contain the task description")
	}
	if !strings.Contains(prompt, "Do not use `ask_user`") {
		t.Error("prompt should contain ask_user exclusion constraint")
	}
}

func TestBuildTeammateSystemPrompt_FullContext(t *testing.T) {
	reg := tool.NewRegistry()
	_ = tool.RegisterBuiltinTools(reg, nil, "/tmp/test-project")

	cfg := &config.Config{Language: "en"}
	ctx := SubAgentPromptContext{
		Cfg:              cfg,
		WorkingDir:       "/tmp/test-project",
		Registry:         reg,
		GitStatus:        func() string { return "## main" },
		RemoteAgentsInfo: func() string { return "" },
	}

	prompt := BuildTeammateSystemPrompt(ctx, "researcher", "review-team", "/tmp/test-project")

	checks := []struct {
		name     string
		contains string
	}{
		{"teammate name", `"researcher"`},
		{"team name", `"review-team"`},
		{"working dir", "/tmp/test-project"},
		{"teammate constraint: bypass", "Permission mode is bypass"},
		{"teammate constraint: no ask_user", "Do not use `ask_user`"},
		{"teammate constraint: no spawn_agent", "spawn_agent"},
		{"teammate constraint: no teammate_spawn", "teammate_spawn"},
		{"VS16 emoji", "Variation Selector-16"},
		{"tool names present", "read_file"},
		{"collaboration guidance", "task board"},
	}

	for _, c := range checks {
		if !strings.Contains(prompt, c.contains) {
			t.Errorf("%s: prompt missing expected text %q", c.name, c.contains)
		}
	}
}

func TestBuildTeammateSystemPrompt_NilFields(t *testing.T) {
	cfg := &config.Config{Language: "en"}
	ctx := SubAgentPromptContext{
		Cfg:        cfg,
		WorkingDir: "/tmp/test",
	}

	prompt := BuildTeammateSystemPrompt(ctx, "coder", "dev-team", "/tmp/test")

	if !strings.Contains(prompt, `"coder"`) {
		t.Error("prompt should contain teammate name")
	}
	if !strings.Contains(prompt, `"dev-team"`) {
		t.Error("prompt should contain team name")
	}
	if !strings.Contains(prompt, "Permission mode is bypass") {
		t.Error("prompt should contain bypass constraint")
	}
}

func TestBuildSubAgentSystemPrompt_NoSlashCommands(t *testing.T) {
	cfg := &config.Config{Language: "en"}
	ctx := SubAgentPromptContext{
		Cfg:        cfg,
		WorkingDir: "/tmp/test",
	}

	prompt := BuildSubAgentSystemPrompt(ctx, "task", "")

	// Sub-agent prompts should NOT include the "## Custom Slash Commands" section
	if strings.Contains(prompt, "## Custom Slash Commands") {
		t.Error("sub-agent prompt should not contain custom slash commands section")
	}
}

func TestBuildSubAgentSystemPrompt_WithMemory(t *testing.T) {
	withTestHome(t)

	cfg := &config.Config{Language: "en"}
	reg := tool.NewRegistry()
	_ = tool.RegisterBuiltinTools(reg, nil, "/tmp/test")

	autoMem := memory.NewAutoMemory()
	projectAutoMem := memory.NewProjectAutoMemory("/tmp/test-ggcode-project")

	ctx := SubAgentPromptContext{
		Cfg:            cfg,
		WorkingDir:     "/tmp/test",
		Registry:       reg,
		GlobalAutoMem:  autoMem,
		ProjectAutoMem: projectAutoMem,
	}

	prompt := BuildSubAgentSystemPrompt(ctx, "task", "")

	// Even with (empty) auto-memory, the prompt should build without error
	// and contain the core constraints.
	if !strings.Contains(prompt, "Permission mode is bypass") {
		t.Error("prompt should contain bypass constraint")
	}
}

// withTestHome isolates HOME so config/memory operations don't pollute the real ~/.ggcode/
func withTestHome(t *testing.T) {
	t.Helper()
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
}
