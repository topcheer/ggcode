package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
)

// handleDebugCommand handles /debug export [category] — exports the debug
// ring buffer to a timestamped file in ~/.ggcode/exports/.
func (m *Model) handleDebugCommand(parts []string) tea.Cmd {
	if len(parts) < 2 || parts[1] != "export" {
		m.chatWriteSystem(nextSystemID(),
			"Usage: /debug export [category]\n\n"+
				"Exports the debug ring buffer (last 2000 entries) to a file.\n"+
				"Optional category filter: agent, session, tunnel, mcp, etc.\n\n"+
				"Example: /debug export        — export all entries\n"+
				"         /debug export agent   — export only agent entries\n"+
				"         /debug export tunnel  — export only tunnel entries")
		m.chatListScrollToBottom()
		return nil
	}

	category := ""
	if len(parts) >= 3 {
		category = strings.TrimSpace(parts[2])
	}

	entries := debug.RingHistoryMax(2000, category)
	if len(entries) == 0 {
		msg := "No debug log entries found"
		if category != "" {
			msg = fmt.Sprintf("No debug log entries found for category '%s'", category)
		}
		m.chatWriteSystem(nextSystemID(), msg)
		m.chatListScrollToBottom()
		return nil
	}

	// Create exports directory
	exportDir := filepath.Join(config.HomeDir(), ".ggcode", "exports")
	if err := os.MkdirAll(exportDir, 0755); err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Failed to create export directory: %v", err))
		m.chatListScrollToBottom()
		return nil
	}

	// Generate filename with timestamp and optional category
	ts := time.Now().Format("20060102-150405")
	suffix := "all"
	if category != "" {
		suffix = category
	}
	filename := fmt.Sprintf("debug-%s-%s.log", ts, suffix)
	path := filepath.Join(exportDir, filename)

	catLabel := "all"
	if category != "" {
		catLabel = category
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# ggcode debug log export\n"))
	sb.WriteString(fmt.Sprintf("# Timestamp: %s\n", time.Now().Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("# Category: %s\n", catLabel))
	sb.WriteString(fmt.Sprintf("# Entries: %d\n\n", len(entries)))

	for _, e := range entries {
		sb.WriteString(fmt.Sprintf("[%s] [%s] %s\n", e.Time, e.Category, e.Message))
	}

	if err := os.WriteFile(path, []byte(sb.String()), 0644); err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Failed to write export: %v", err))
		m.chatListScrollToBottom()
		return nil
	}

	m.chatWriteSystem(nextSystemID(),
		fmt.Sprintf("Debug log exported: %d entries → %s", len(entries), path))
	m.chatListScrollToBottom()
	return nil
}
