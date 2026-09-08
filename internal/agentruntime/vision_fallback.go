package agentruntime

import (
	"strings"

	"github.com/topcheer/ggcode/internal/config"
)

// VisionTurnModel selects a vision-capable model from cfg's ACTIVE endpoint
// for a turn-scoped switch. Returns "" when the active model already supports
// vision (no switch needed) or no comparable candidate exists. The reference
// window is the active model's context window, mirroring SelectVisionModel.
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

// SelectVisionModel picks a vision-capable model from the given model list
// for a turn-scoped switch when the user's model cannot accept images.
//
// Selection rule ("comparable context window"): among candidates, prefer
// those whose inferred context window is >= referenceWindow (typically the
// user's active model window) - smallest qualifying window wins. When NO
// candidate meets the bar, the largest-window vision candidate wins as a
// last resort: a slightly smaller window may still hold the session, and a
// genuine overflow degrades via the 400 text-only fallback - rejecting the
// switch outright is what fed the image-400 retry loop. Models with unknown
// windows are treated as the 128k default. Returns "" only when the list
// has no vision-capable model at all.
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
