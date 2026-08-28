package agentruntime

import (
	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
)

// ApplyVerifyConfigToAgent enables the post-loop automatic verification pass
// when config verify.auto_after_run is set. Default (unset/false) keeps the
// pass disabled: the system prompt already mandates in-loop scoped
// verification, which current models handle without a redundant end-of-loop
// whole-pass.
func ApplyVerifyConfigToAgent(agentInst *agent.Agent, cfg *config.Config) {
	if agentInst == nil || cfg == nil {
		return
	}
	if cfg.Verify.AutoAfterRun {
		agentInst.SetAutoVerify(true)
	}
	// Claims-supervision is default-off at the Agent level; mirror the config
	// so the opt-in re-enables the detector family on main agents only.
	agentInst.SetClaimsSupervision(cfg.Verify.ClaimsSupervision)
}
