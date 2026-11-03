package agentruntime

import (
	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/tool"
)

func RegisterCompactContextTool(registry *tool.Registry, ag *agent.Agent) {
	if registry == nil {
		return
	}
	_ = registry.Register(&tool.CompactContextTool{
		Requester: func(reason string) string {
			if ag == nil {
				return ""
			}
			return ag.RequestAutoCompact(reason)
		},
	})
}
