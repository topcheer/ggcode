package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

func combineCmds(cmds ...tea.Cmd) tea.Cmd {
	filtered := make([]tea.Cmd, 0, len(cmds))
	for _, cmd := range cmds {
		if cmd != nil {
			filtered = append(filtered, cmd)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	if len(filtered) == 1 {
		return filtered[0]
	}
	return tea.Batch(filtered...)
}

// looksLikeStartupGarbage checks whether the input value accumulated before
// setProgramMsg looks like terminal response garbage rather than legitimate
// user input. Terminal responses contain ASCII control characters ([, ], digits,
// and punctuation like ;, :, /) that don't appear in normal typing.
// Normal pre-set values like "ping" contain only alphabetic characters.
func looksLikeStartupGarbage(val string) bool {
	for _, r := range val {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == ' ' || r == '-' || r == '_':
			// Common in normal text, keep going
		default:
			// Contains characters not found in normal typing:
			// ; : / [ ] $ ? = etc. — terminal response artifacts
			return true
		}
	}
	return false
}

func startupInputSuppressionActive(startedAt time.Time) bool {
	if startedAt.IsZero() {
		return false
	}
	return time.Since(startedAt) <= startupInputGateWindow
}
