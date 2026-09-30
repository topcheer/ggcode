package agent

import (
	"encoding/json"

	"github.com/topcheer/ggcode/internal/similarity"
)

// Shared command/similarity helpers. These were originally defined inside
// the removed verify_suppress.go and prompt_ops.go detectors but are still
// used by retained detectors (scope narrowing, reasoning redundancy).

// extractCommandFromToolCall extracts the command string from a tool call's
// arguments.
func extractCommandFromToolCall(args json.RawMessage) string {
	return extractCommandFromArgs(args)
}

// jaccardSimilarity computes the Jaccard similarity between two word sets.
func jaccardSimilarity(a, b map[string]bool) float64 {
	return similarity.Jaccard(a, b)
}
