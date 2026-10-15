package agentruntime

// recall_compacted wiring: binds the agent-invocable compacted-content
// search entry point to the tool registry in both assembly paths
// (interactive root.go and headless pipe.go). Late-binding, same pattern
// as RegisterCompactContextTool: the searcher closes over the agent
// variable, which may still be nil at registration time.

import (
	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/tool"
)

// RegisterRecallCompactedTool registers the recall_compacted tool and binds
// it to ag's RecallCompacted entry point. Safe to call with a nil registry
// or nil agent; duplicate registration errors are ignored, matching the
// other agentruntime Register* helpers.
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
