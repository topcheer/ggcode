package agentruntime

import (
	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/tool"
)

func RegisterRecallCompactedTool(registry *tool.Registry, ag *agent.Agent) {
	if registry == nil {
		return
	}
	_ = registry.Register(&tool.RecallCompactedTool{
		Searcher: func(query string, limit int) string {
			if ag == nil {
				return ""
			}
			return ag.RecallCompacted(query, limit)
		},
	})
}
