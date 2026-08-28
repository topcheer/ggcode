package agentruntime

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
)

// TestSubagentNeverEnablesPostLoopVerify locks the invariant that the
// post-loop verification pass is NEVER enabled on subagent instances:
// ApplyVerifyConfigToAgent is wired only at the two main-agent construction
// sites (cmd/ggcode/root.go, cmd/ggcode/pipe.go). Subagent construction
// paths (internal/tui/repl.go, internal/a2a/handler.go, agentruntime) never
// call it, so even a user opting in via verify.auto_after_run must not leak
// the whole-pass (or its phantom-failure injection loop) into subagents.
func TestSubagentNeverEnablesPostLoopVerify(t *testing.T) {
	// Default: any freshly constructed agent has the pass off.
	sub := agent.NewAgent(nil, nil, "", 10)
	if sub.AutoVerifyEnabled() {
		t.Fatal("fresh agent must default to post-loop verification disabled")
	}
	if sub.ClaimsSupervisionEnabled() {
		t.Fatal("fresh agent must default to claims-supervision detectors disabled")
	}

	// Claims supervision: opt-in config re-enables the family on the agent
	// the applier is pointed at, and only that agent.
	claimsOn := &config.Config{}
	claimsOn.Verify.ClaimsSupervision = true
	supervised := agent.NewAgent(nil, nil, "", 10)
	ApplyVerifyConfigToAgent(supervised, claimsOn)
	if !supervised.ClaimsSupervisionEnabled() {
		t.Fatal("applier must enable claims supervision when verify.claims_supervision=true")
	}
	unsupervised := agent.NewAgent(nil, nil, "", 10)
	ApplyVerifyConfigToAgent(unsupervised, nil) // nil cfg: no change
	if unsupervised.ClaimsSupervisionEnabled() {
		t.Fatal("claims supervision must stay off without opt-in")
	}

	// Opt-in config exists in the workspace...
	cfg := &config.Config{}
	cfg.Verify.AutoAfterRun = true
	// ...and the applier itself works when explicitly invoked (main agent path):
	main := agent.NewAgent(nil, nil, "", 10)
	ApplyVerifyConfigToAgent(main, cfg)
	if !main.AutoVerifyEnabled() {
		t.Fatal("applier must enable the pass when verify.auto_after_run=true")
	}

	// ...but subagent construction (which never routes through the applier)
	// keeps the pass off even though the same cfg object is in scope.
	another := agent.NewAgent(nil, nil, "", 10) // subagent-style: no ApplyVerifyConfigToAgent call
	ApplyVerifyConfigToAgent(nil, cfg)          // nil-agent guard must be a no-op
	ApplyVerifyConfigToAgent(another, nil)      // nil-cfg guard must be a no-op
	if another.AutoVerifyEnabled() {
		t.Fatal("subagent instances must never enable post-loop verification, even with opt-in config present")
	}
}

// TestSubagentPromptCarriesInLoopVerifyMandate pins the other half of the
// contract: with the post-loop pass off, the subagent system prompt (built
// via buildSharedAgentPrompt -> config.BuildSystemPrompt) still mandates
// in-loop scoped verification. This prevents a regression where subagents
// end up with neither the prompt mandate nor the post-loop pass.
func TestSubagentPromptCarriesInLoopVerifyMandate(t *testing.T) {
	prompt := buildSharedAgentPrompt(SubAgentPromptContext{
		WorkingDir: t.TempDir(),
	})
	if !strings.Contains(prompt, "narrowest existing validation") {
		t.Error("subagent prompt must carry the in-loop scoped verification mandate")
	}
}
