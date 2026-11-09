package agentruntime

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/memory"
)

// v2 amendment: pointer-only injection. The pointer section is the only
// memory surface in any prompt, no matter how many entries the stores hold.
func TestAppendAutoMemoryPointerOnly(t *testing.T) {
	withTestHome(t)
	global := memory.NewAutoMemory()
	if err := global.SaveMemory("build-process", "run make verify-ci"); err != nil {
		t.Fatal(err)
	}
	project := memory.NewProjectAutoMemory(t.TempDir())
	if project != nil {
		if err := project.SaveMemoryWithSource("deploy-impl", "docker push registry.example/team/app", "test"); err != nil {
			t.Fatal(err)
		}
	}
	for _, task := range []string{"", "deploy the app", "build-process related task"} {
		prompt := appendAutoMemory("BASE", global, project, task)
		if !strings.Contains(prompt, memoryRecallPointerSection) {
			t.Fatalf("pointer section missing (task=%q)", task)
		}
		for _, leak := range []string{"run make verify-ci", "docker push registry.example", "## Auto Memory", "Memory Repair"} {
			if strings.Contains(prompt, leak) {
				t.Fatalf("memory leaked into prompt (task=%q): %q", task, leak)
			}
		}
	}
	// nil stores: pointer still present, zero drift.
	if got := appendAutoMemory("BASE", nil, nil, ""); !strings.Contains(got, memoryRecallPointerSection) {
		t.Fatal("pointer section missing with nil stores")
	}
}

// Both interactive and sub-agent build paths render the pointer.
func TestBuildPathsRenderPointer(t *testing.T) {
	withTestHome(t)
	autoMem := memory.NewAutoMemory()
	if err := autoMem.SaveMemory("build-process", "run make verify-ci"); err != nil {
		t.Fatal(err)
	}
	sub := BuildSubAgentSystemPrompt(SubAgentPromptContext{
		Cfg:           &config.Config{},
		WorkingDir:    "/tmp/test",
		GlobalAutoMem: autoMem,
	}, "", "")
	if !strings.Contains(sub, memoryRecallPointerSection) || strings.Contains(sub, "run make verify-ci") {
		t.Fatal("sub-agent prompt must carry the pointer and no memory bodies")
	}
}
