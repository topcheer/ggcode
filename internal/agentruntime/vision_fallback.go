package agentruntime

import (
	"strings"

	"github.com/topcheer/ggcode/internal/config"
)

func VisionTurnModel(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	resolved, err := cfg.ResolveActiveEndpoint()
	if err != nil || resolved == nil || resolved.SupportsVision {
		return ""
	}
	vc, ok := cfg.Vendors[cfg.Vendor]
	if !ok {
		return ""
	}
	ep, ok := vc.Endpoints[cfg.Endpoint]
	if !ok || len(ep.Models) == 0 {
		return ""
	}
	return SelectVisionModel(ep.Models, resolved.ContextWindow)
}

func SelectVisionModel(models []string, referenceWindow int) string {
	const unknownWindowFallback = 128000
	best := ""
	bestWindow := 0
	lastResort := ""
	lastResortWindow := 0
	for _, m := range models {
		m = strings.TrimSpace(m)
		if m == "" || !config.ModelSupportsVision(m) {
			continue
		}
		w := config.ModelContextWindow(m)
		if w <= 0 {
			w = unknownWindowFallback
		}
		if (referenceWindow <= 0 || w >= referenceWindow) && (best == "" || w < bestWindow) {
			best, bestWindow = m, w
			continue
		}
		if w > lastResortWindow {
			lastResort, lastResortWindow = m, w
		}
	}
	if best != "" {
		return best
	}
	return lastResort
}
