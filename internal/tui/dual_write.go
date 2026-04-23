package tui

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/chat"
)

// dualWriteSystem writes a pre-rendered system/tool string to all paths.
func (m *Model) dualWriteSystem(text string) {
	// Legacy path
	m.bridgeDualWriteSystem(text)
	// Also add to chatList as a system item
	if m.chatList != nil && strings.TrimSpace(text) != "" {
		item := chat.NewSystemItem(nextChatID(), stripAnsiForChat(text), m.chatStyles)
		m.chatList.Append(item)
	}
}

// sysf writes a formatted system line to all output paths.
func (m *Model) sysf(format string, args ...interface{}) {
	text := fmt.Sprintf(format, args...)
	m.dualWriteSystem(text)
}

// stripAnsiForChat removes ANSI escape codes from text before storing in chatList.
// chatList items use their own styling — ANSI from legacy code would interfere.
func stripAnsiForChat(s string) string {
	var result strings.Builder
	inEscape := false
	for _, c := range s {
		if c == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if c == 'm' {
				inEscape = false
			}
			continue
		}
		result.WriteRune(c)
	}
	return result.String()
}
