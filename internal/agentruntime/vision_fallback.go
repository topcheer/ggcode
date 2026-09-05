package agentruntime

import (
	"strings"

	"github.com/topcheer/ggcode/internal/config"
)

// SelectVisionModel picks a vision-capable model from the given model list
// for a turn-scoped switch when the user's model cannot accept images.
//
// Selection rule ("comparable context window"): a candidate qualifies only
// when its inferred context window is >= referenceWindow (typically the
// user's active model window) - otherwise the existing conversation would
// overflow the smaller window and the request would likely fail. Among
// qualifying candidates the smallest window wins (closest to the user's
// model, minimizing cost/latency jump). Models with unknown windows are
// treated as the 128k default. Returns "" when no candidate qualifies;
// callers should then keep the current model and strip images instead.
func SelectVisionModel(models []string, referenceWindow int) string {
	const unknownWindowFallback = 128000
	best := ""
	bestWindow := 0
	for _, m := range models {
		m = strings.TrimSpace(m)
		if m == "" || !config.ModelSupportsVision(m) {
			continue
		}
		w := config.ModelContextWindow(m)
		if w <= 0 {
			w = unknownWindowFallback
		}
		if referenceWindow > 0 && w < referenceWindow {
			continue // too small - the conversation would not fit
		}
		if best == "" || w < bestWindow {
			best, bestWindow = m, w
		}
	}
	return best
}
