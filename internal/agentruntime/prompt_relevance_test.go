package agentruntime

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/memory"
)

// v2 amendment: pointer-only injection. Relevance wiring no longer has a
// prompt surface — the store is searched on demand via memory_recall. This
// pin guards that NEITHER related NOR unrelated memory bodies/keys leak
// into the sub-agent prompt, regardless of task relevance.
func TestSubAgentPromptMemoryNeverInjected(t *testing.T) {
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
		Cfg:            &config.Config{},
		ProjectAutoMem: am,
	}
	prompt := BuildSubAgentSystemPrompt(ctx, "fix the flaky postgres migration test", "")

	if !strings.Contains(prompt, memoryRecallPointerSection) {
		t.Fatal("memory recall pointer must be present")
	}
	for _, leak := range []string{
		"postgres-migration-impl",      // related key: still must not inject
		"docker-deploy-impl",           // unrelated key
		"docker push registry.example", // any body
		"## Auto Memory",               // legacy section header
	} {
		if strings.Contains(prompt, leak) {
			t.Fatalf("memory must not leak into the prompt (pointer-only): %q", leak)
		}
	}
}
