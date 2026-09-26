package agentruntime

// Compact-context (CAT) wiring: binds the agent-invocable context reclaim
// entry point to the tool registry in both assembly paths (interactive
// root.go and headless pipe.go). Late-binding: the requester closes over the
// agent variable, which may still be nil at registration time; the closure
// is only invoked when the model calls the tool, by which point the agent
// is fully constructed in every current call path.

import (
	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/tool"
)

// RegisterCompactContextTool registers the compact_context tool and binds it
// to ag's RequestAutoCompact entry point. Safe to call with a nil registry
// or nil agent (the tool then reports itself unavailable); registration is
// idempotent-tolerant (duplicate registration errors are ignored, matching
// the other agentruntime Register* helpers).
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
