package agentruntime

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/tool"
)

func TestBuildSubAgentSystemPrompt_FullContext(t *testing.T) {

	reg := tool.NewRegistry()
	_ = tool.RegisterBuiltinTools(reg, nil, "/tmp/test-project", nil, nil)

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

	checks := []struct {
		name     string
		contains string
	}{
		{"working dir", "/tmp/test-project"},
		{"task", "review the code"},
		{"sub-agent constraint: bypass", "Permission mode is bypass"},
		{"sub-agent constraint: no ask_user", "`ask_user` is not available"},
		{"sub-agent constraint: no spawn", "`spawn_agent` is not available"},
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
	ctx := SubAgentPromptContext{
		WorkingDir: "/tmp/test",
	}

	prompt := BuildSubAgentSystemPrompt(ctx, "do something", "")

	if !strings.Contains(prompt, "Permission mode is bypass") {
		t.Error("prompt should contain bypass constraint even with nil fields")
	}
	if !strings.Contains(prompt, "do something") {
		t.Error("prompt should contain the task description")
	}
	if !strings.Contains(prompt, "`ask_user` is not available") {
		t.Error("prompt should contain ask_user exclusion constraint")
	}
}

func TestBuildTeammateSystemPrompt_FullContext(t *testing.T) {
	reg := tool.NewRegistry()
	_ = tool.RegisterBuiltinTools(reg, nil, "/tmp/test-project", nil, nil)

	cfg := &config.Config{Language: "en"}
	ctx := SubAgentPromptContext{
		Cfg:              cfg,
		WorkingDir:       "/tmp/test-project",
		Registry:         reg,
		GitStatus:        func() string { return "## main" },
		RemoteAgentsInfo: func() string { return "" },
	}

	prompt := BuildTeammateSystemPrompt(ctx, "researcher", "review-team")

	checks := []struct {
		name     string
		contains string
	}{
		{"teammate name", `"researcher"`},
		{"team name", `"review-team"`},
		{"working dir", "/tmp/test-project"},
		{"teammate constraint: bypass", "Permission mode is bypass"},
		{"teammate constraint: no ask_user", "`ask_user` is not available"},
		{"teammate constraint: no spawn_agent", "`spawn_agent`"},
		{"teammate constraint: no teammate_spawn", "`teammate_spawn`"},
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
	ctx := SubAgentPromptContext{
		WorkingDir: "/tmp/test",
	}

	prompt := BuildTeammateSystemPrompt(ctx, "coder", "dev-team")

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
	ctx := SubAgentPromptContext{
		WorkingDir: "/tmp/test",
	}

	prompt := BuildSubAgentSystemPrompt(ctx, "task", "")

	if strings.Contains(prompt, "## Custom Slash Commands") {
		t.Error("sub-agent prompt should not contain custom slash commands section")
	}
}

func TestBuildSubAgentSystemPrompt_WithMemory(t *testing.T) {
	withTestHome(t)

	cfg := &config.Config{Language: "en"}
	reg := tool.NewRegistry()
	_ = tool.RegisterBuiltinTools(reg, nil, "/tmp/test", nil, nil)

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

	if !strings.Contains(prompt, "Permission mode is bypass") {
		t.Error("prompt should contain bypass constraint")
	}
}

func TestBuildSubAgentSystemPrompt_DeterministicToolOrder(t *testing.T) {
	reg := tool.NewRegistry()
	_ = tool.RegisterBuiltinTools(reg, nil, "/tmp/test", nil, nil)

	ctx := SubAgentPromptContext{
		WorkingDir: "/tmp/test",
		Registry:   reg,
	}

	prompt1 := BuildSubAgentSystemPrompt(ctx, "task", "")
	prompt2 := BuildSubAgentSystemPrompt(ctx, "task", "")

	if prompt1 != prompt2 {
		t.Error("prompt should be deterministic (same tools should produce same order)")
	}
}

func TestBuildSubAgentSystemPrompt_MemoryFraming(t *testing.T) {
	withTestHome(t)

	cfg := &config.Config{Language: "en"}
	reg := tool.NewRegistry()
	_ = tool.RegisterBuiltinTools(reg, nil, "/tmp/test", nil, nil)

	autoMem := memory.NewAutoMemory()

	ctx := SubAgentPromptContext{
		Cfg:           cfg,
		WorkingDir:    "/tmp/test",
		Registry:      reg,
		GlobalAutoMem: autoMem,
	}

	prompt := BuildSubAgentSystemPrompt(ctx, "task", "")

	if strings.Contains(prompt, "## Auto Memory") {
		if !strings.Contains(prompt, "reference information") {
			t.Error("auto-memory section should contain framing text indicating it's reference data")
		}
	}
}

func withTestHome(t *testing.T) {
	t.Helper()
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
}

func TestBuildSubAgentSystemPrompt_ExploreContract(t *testing.T) {
	reg := tool.NewRegistry()
	_ = tool.RegisterBuiltinTools(reg, nil, "/tmp/test-project", nil, nil)
	ctx := SubAgentPromptContext{Cfg: &config.Config{Language: "en"}, WorkingDir: "/tmp/test-project", Registry: reg}

	explore := BuildSubAgentSystemPrompt(ctx, "find the retry logic", "Explore")
	if !strings.Contains(explore, "## Output Contract (Explore)") || !strings.Contains(explore, "## Regions") {
		t.Error("Explore prompt missing output contract")
	}

	other := BuildSubAgentSystemPrompt(ctx, "fix the bug", "Plan")
	if strings.Contains(other, "## Output Contract (Explore)") {
		t.Error("non-Explore prompt must not carry the Explore contract")
	}
}

// #5340: the shared-workspace agents section is appended verbatim when
// provided and omitted entirely when empty (zero drift for solo runs).
func TestBuildInteractiveSystemPromptSharedWorkspaceAgents(t *testing.T) {
	section := "## Shared workspace agents\n\n- alpha (ggcode): busy\n"
	with := BuildInteractiveSystemPrompt(&config.Config{}, "/w", permission.SupervisedMode, nil, nil, nil, nil, "", "", section)
	if !strings.Contains(with, section) {
		t.Fatal("section missing from prompt when provided")
	}
	without := BuildInteractiveSystemPrompt(&config.Config{}, "/w", permission.SupervisedMode, nil, nil, nil, nil, "", "", "")
	if strings.Contains(without, "Shared workspace agents") {
		t.Fatal("section must be omitted when empty")
	}
}
