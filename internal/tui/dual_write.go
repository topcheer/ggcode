package tui

import "fmt"

// dualWrite appends to both the legacy output buffer and the new chatEntries list.
// This is the migration bridge: all callers that previously only wrote to
// m.output should use this instead. Once the migration is complete,
// m.output can be removed.
func (m *Model) dualWrite(entry ChatEntry) {
	// Write rendered content to legacy output buffer
	if entry.Prefix != "" && (entry.Role == "user" || entry.Role == "assistant") {
		m.output.WriteString(m.renderConversationUserEntry(entry.Prefix, entry.RawText))
		m.output.WriteString("\n")
	} else if entry.Role == "assistant" {
		// Pure markdown, no prefix — will be rendered later
	} else {
		m.output.WriteString(entry.RawText)
	}
	m.chatEntries.Append(entry)
}

// dualWriteSystem writes a pre-rendered system/tool string to both paths.
func (m *Model) dualWriteSystem(text string) {
	m.output.WriteString(text)
	m.chatEntries.Append(ChatEntry{Role: "system", RawText: text})
}

// sysf writes a formatted system line to both output paths.
// Equivalent to m.output.WriteString(fmt.Sprintf(format, args...))
// but also records in chatEntries for deferred rendering.
func (m *Model) sysf(format string, args ...interface{}) {
	text := fmt.Sprintf(format, args...)
	m.output.WriteString(text)
	m.chatEntries.Append(ChatEntry{Role: "system", RawText: text})
}
