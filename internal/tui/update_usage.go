package tui

import (
	"github.com/topcheer/ggcode/internal/usage"
)

// ensureUsageService lazily builds the shared usage Service with the
// built-in probes registered (#2150 batch 2). Adapters are value types;
// registration is idempotent so re-calls after a refresh reset are safe.
func (m *Model) ensureUsageService() *usage.Service {
	if m.usageService == nil {
		svc := usage.NewService()
		svc.Register(usage.ZaiProbe{})
		svc.Register(usage.DeepSeekProbe{})
		svc.Register(usage.MoonshotProbe{})
		m.usageService = svc
	}
	return m.usageService
}
