package agent

import "encoding/json"

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
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	intersection := 0
	for w := range a {
		if b[w] {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}
