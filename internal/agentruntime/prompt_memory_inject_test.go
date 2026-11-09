package agentruntime

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/tool"
)

// mkInjectFixtures builds a project memory store with one entry plus a
// minimal config/registry pair for prompt-injection tests.
func mkInjectFixtures(t *testing.T) (*config.Config, *memory.AutoMemory, *memory.AutoMemory) {
	t.Helper()
	withTestHome(t)
	global := memory.NewAutoMemory()
	project := memory.NewProjectAutoMemory(t.TempDir())
	if err := project.SaveMemory("build-process", "marker-run-make-verify-ci-before-push"); err != nil {
		t.Fatal(err)
	}
	if err := global.SaveMemory("coord-decision", "marker-global-coordinator-discipline"); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Language: "en"}
	return cfg, global, project
}

func TestAppendAutoMemory_PointerModeInjectsOnlyPointer(t *testing.T) {
	_, global, project := mkInjectFixtures(t)

	prompt := appendAutoMemory("BASE", global, project, "", "pointer")

	if !strings.Contains(prompt, "## Memory") || !strings.Contains(prompt, "memory_recall") {
		t.Fatalf("pointer section missing:\n%s", prompt)
	}
	for _, banned := range []string{"## Auto Memory", "build-process", "coord-decision", "marker-run-make-verify-ci", "marker-global-coordinator"} {
		if strings.Contains(prompt, banned) {
			t.Fatalf("pointer mode leaked %q into the prompt:\n%s", banned, prompt)
		}
	}
}

func TestAppendAutoMemory_PointerModeWithNilMemoriesStillInjectsPointer(t *testing.T) {
	prompt := appendAutoMemory("BASE", nil, nil, "", "pointer")
	if !strings.Contains(prompt, "memory_recall") {
		t.Fatalf("pointer must be injected even with no memories:\n%s", prompt)
	}
}

func TestAppendAutoMemory_FullModeKeepsLegacyInjection(t *testing.T) {
	_, global, project := mkInjectFixtures(t)

	prompt := appendAutoMemory("BASE", global, project, "", "full")

	for _, want := range []string{"## Auto Memory", "build-process", "marker-run-make-verify-ci", "coord-decision", "Reference data at the same trust level"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("full mode missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "Durable memories (global + project) are NOT injected here") {
		t.Fatalf("full mode must not carry the pointer line:\n%s", prompt)
	}
}

func TestBuildSubAgentSystemPrompt_PointerModeByDefault(t *testing.T) {
	cfg, global, project := mkInjectFixtures(t)
	reg := tool.NewRegistry()
	_ = tool.RegisterBuiltinTools(reg, nil, "/tmp/test", nil, nil)

	prompt := BuildSubAgentSystemPrompt(SubAgentPromptContext{
		Cfg:            cfg,
		WorkingDir:     "/tmp/test",
		Registry:       reg,
		GlobalAutoMem:  global,
		ProjectAutoMem: project,
	}, "", "")

	if !strings.Contains(prompt, "memory_recall") {
		t.Fatal("prompt should carry the memory_recall pointer by default")
	}
	if strings.Contains(prompt, "build-process") || strings.Contains(prompt, "marker-run-make-verify-ci") {
		t.Fatal("default (pointer) mode must not inject memory keys or bodies")
	}
}

func TestBuildInteractiveSystemPrompt_PointerAndFullModes(t *testing.T) {
	cfg, global, project := mkInjectFixtures(t)
	reg := tool.NewRegistry()
	_ = tool.RegisterBuiltinTools(reg, nil, "/tmp/test", nil, nil)

	pointer := BuildInteractiveSystemPrompt(cfg, "/tmp/test", 0, reg, nil, global, project, "", "", "")
	if !strings.Contains(pointer, "memory_recall") || strings.Contains(pointer, "marker-run-make-verify-ci") {
		t.Fatalf("interactive prompt should use pointer mode by default:\n%.400s", pointer)
	}

	cfgFull := &config.Config{Language: "en"}
	cfgFull.Memory.PromptInject = "full"
	full := BuildInteractiveSystemPrompt(cfgFull, "/tmp/test", 0, reg, nil, global, project, "", "", "")
	if !strings.Contains(full, "## Auto Memory") || !strings.Contains(full, "marker-run-make-verify-ci") {
		t.Fatalf("full-mode interactive prompt should inline memories:\n%.400s", full)
	}
}

func TestConfigMemoryPromptInjectMode(t *testing.T) {
	var nilCfg *config.Config
	if nilCfg.MemoryPromptInjectMode() != "pointer" {
		t.Fatal("nil config must default to pointer")
	}
	cfg := &config.Config{}
	for _, val := range []string{"", "pointer", "POINTER", "garbage"} {
		cfg.Memory.PromptInject = val
		if got := cfg.MemoryPromptInjectMode(); got != "pointer" {
			t.Fatalf("prompt_inject=%q should resolve to pointer, got %q", val, got)
		}
	}
	cfg.Memory.PromptInject = "full"
	if got := cfg.MemoryPromptInjectMode(); got != "full" {
		t.Fatalf("prompt_inject=full should resolve to full, got %q", got)
	}
	cfg.Memory.PromptInject = "FULL"
	if got := cfg.MemoryPromptInjectMode(); got != "full" {
		t.Fatalf("prompt_inject=FULL should resolve to full, got %q", got)
	}
}
