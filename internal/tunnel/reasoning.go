package tunnel

import "strings"

const (
	RedactedReasoningSentinel    = "__redacted_thinking__"
	RedactedReasoningPlaceholder = "Reasoning hidden by model."
)

func NormalizeReasoningChunk(text string) string {
	switch strings.TrimSpace(text) {
	case "":
		return ""
	case RedactedReasoningSentinel:
		return RedactedReasoningPlaceholder
	default:
		return text
	}
}
