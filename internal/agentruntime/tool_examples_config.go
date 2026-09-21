package agentruntime

import (
	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
)

// ApplyToolExamplesConfigToAgent wires config tool_examples into the agent:
// exact tool name -> sample invocations attached to outbound definitions
// (Anthropic `input_examples` natively; compact description suffix on
// providers without native support). Default (unset/empty) keeps outbound
// tool definitions byte-identical.
func ApplyToolExamplesConfigToAgent(agentInst *agent.Agent, cfg *config.Config) {
	if agentInst == nil || cfg == nil || len(cfg.ToolExamples) == 0 {
		return
	}
	agentInst.SetToolExamples(cfg.ToolExamples)
}
