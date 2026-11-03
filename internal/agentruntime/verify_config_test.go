package agentruntime

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
)

func TestSubagentNeverEnablesPostLoopVerify(t *testing.T) {

	sub := agent.NewAgent(nil, nil, "", 10)
	if sub.AutoVerifyEnabled() {
		t.Fatal("fresh agent must default to post-loop verification disabled")
	}
	if sub.ClaimsSupervisionEnabled() {
		t.Fatal("fresh agent must default to claims-supervision detectors disabled")
	}

	claimsOn := &config.Config{}
	claimsOn.Verify.ClaimsSupervision = true
	supervised := agent.NewAgent(nil, nil, "", 10)
	ApplyVerifyConfigToAgent(supervised, claimsOn)
	if !supervised.ClaimsSupervisionEnabled() {
		t.Fatal("applier must enable claims supervision when verify.claims_supervision=true")
	}
	unsupervised := agent.NewAgent(nil, nil, "", 10)
	ApplyVerifyConfigToAgent(unsupervised, nil)
	if unsupervised.ClaimsSupervisionEnabled() {
		t.Fatal("claims supervision must stay off without opt-in")
	}

	cfg := &config.Config{}
	cfg.Verify.AutoAfterRun = true

	main := agent.NewAgent(nil, nil, "", 10)
	ApplyVerifyConfigToAgent(main, cfg)
	if !main.AutoVerifyEnabled() {
		t.Fatal("applier must enable the pass when verify.auto_after_run=true")
	}

	another := agent.NewAgent(nil, nil, "", 10)
	ApplyVerifyConfigToAgent(nil, cfg)
	ApplyVerifyConfigToAgent(another, nil)
	if another.AutoVerifyEnabled() {
		t.Fatal("subagent instances must never enable post-loop verification, even with opt-in config present")
	}
}

func TestSubagentPromptCarriesInLoopVerifyMandate(t *testing.T) {
	prompt := buildSharedAgentPrompt(SubAgentPromptContext{
		WorkingDir: t.TempDir(),
	}, "")
	if !strings.Contains(prompt, "narrowest existing validation") {
		t.Error("subagent prompt must carry the in-loop scoped verification mandate")
	}
}
