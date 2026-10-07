package agentruntime

// sa-113 end-to-end wiring smoke: a sub-agent prompt built with a task
// must not inline persistent memories lexically unrelated to that task,
// while the index keeps them retrievable.

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/memory"
)

func TestSubAgentPromptMemoryRelevanceWiring(t *testing.T) {
	dir := t.TempDir()
	am := memory.NewProjectAutoMemory(dir)
	if am == nil {
		t.Skip("NewProjectAutoMemory unavailable in this environment")
	}
	if err := am.SaveMemoryWithSource("docker-deploy-impl", "deploy: docker push registry.example/team/app", "test"); err != nil {
		t.Fatal(err)
	}
	if err := am.SaveMemoryWithSource("postgres-migration-impl", "run migrations with postgres psql before cutover", "test"); err != nil {
		t.Fatal(err)
	}

	ctx := SubAgentPromptContext{
		WorkingDir:     dir,
		ProjectAutoMem: am,
	}
	prompt := BuildSubAgentSystemPrompt(ctx, "fix the flaky postgres migration test", "")

	if !strings.Contains(prompt, "postgres-migration-impl") {
		t.Fatal("related memory must be present in the sub-agent prompt")
	}
	// The unrelated docker memory must not be inlined (full body absent)
	// but its key stays in the index section for read_file retrieval.
	if strings.Contains(prompt, "docker push registry.example") {
		t.Fatal("unrelated memory body must not be inlined into the sub-agent prompt")
	}
	if !strings.Contains(prompt, "docker-deploy-impl") {
		t.Fatal("unrelated memory key must remain in the index for read_file retrieval")
	}
}
