package tui

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/chat"
)

// dualWriteSystem writes a pre-rendered system/tool string to chatList.
func (m *Model) dualWriteSystem(text string) {
	if m.chatList == nil {
		return
	}
	clean := stripAnsiForChat(text)
	if strings.TrimSpace(clean) == "" {
		return
	}
	// Try to merge with the last system item to avoid fragmentation
	lastIdx := m.chatList.Len() - 1
	if lastIdx >= 0 {
		if si, ok := m.chatList.ItemAt(lastIdx).(*chat.SystemItem); ok {
			si.AppendText(clean)
			return
		}
	}
	item := chat.NewSystemItem(nextChatID(), clean, m.chatStyles)
	m.chatList.Append(item)
}

// sysf writes a formatted system line to chatList.
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
