package agentruntime

import (
	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
)

func ApplyVerifyConfigToAgent(agentInst *agent.Agent, cfg *config.Config) {
	if agentInst == nil || cfg == nil {
		return
	}
	if cfg.Verify.AutoAfterRun {
		agentInst.SetAutoVerify(true)
	}

	agentInst.SetClaimsSupervision(cfg.Verify.ClaimsSupervision)

	if cfg.Verify.AdversarialReview {
		agentInst.SetAdversarialReview(true)
	}
}
