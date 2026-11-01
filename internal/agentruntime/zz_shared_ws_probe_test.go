package agentruntime

import (
	"strings"
	"testing"
)

// #4915: when the daemon registers a shared-workspace probe, both prompt
// builders must inject its text right after the base system prompt; with no
// probe (single-agent default) prompts stay unchanged.
func TestSharedWorkspaceProbeInjectedIntoPrompts(t *testing.T) {
	SetSharedWorkspaceProbe(func() string {
		return "## SHARED WORKSPACE — CONCURRENT AGENTS ACTIVE\npeer-x is here"
	})
	t.Cleanup(func() { SetSharedWorkspaceProbe(nil) })

	t.Run("sub agent prompt", func(t *testing.T) {
		got := BuildSubAgentSystemPrompt(SubAgentPromptContext{WorkingDir: "/tmp/x"}, "", "")
		if !strings.Contains(got, "SHARED WORKSPACE — CONCURRENT AGENTS ACTIVE") || !strings.Contains(got, "peer-x") {
			t.Fatalf("sub-agent prompt missing shared-workspace alert, tail:\n%s", lastLines(got, 5))
		}
	})
	t.Run("no probe = unchanged", func(t *testing.T) {
		SetSharedWorkspaceProbe(nil)
		got := BuildSubAgentSystemPrompt(SubAgentPromptContext{WorkingDir: "/tmp/x"}, "", "")
		if strings.Contains(got, "SHARED WORKSPACE") {
			t.Fatal("prompt must not mention shared workspace without a probe")
		}
	})
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
