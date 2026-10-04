package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// /tape commands: TUI entry point for the deterministic tool record/replay
// harness (internal/toolreplay, wired in internal/agent/tool_tape.go).
//
// The tape mode is resolved once at agent construction from
// GGCODE_TOOL_TAPE, so start/stop write the env var and require /restart
// (which rebuilds the agent) to take effect. status reads the live state.
//
//	/tape start [path]   record every tool call to a tape file (default
//	                     ~/.ggcode/tapes/session-<timestamp>.tape.json)
//	/tape replay <path>  serve tool results from a recorded tape (no real
//	                     tool side effects; a tape miss is an explicit error)
//	/tape stop           clear the env var (disables tape after /restart)
//	/tape status         show current mode, tape file, and entry count

// defaultTapePath returns the tape file path used when /tape start is given
// no explicit path.
func defaultTapePath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "."
	}
	return filepath.Join(home, ".ggcode", "tapes",
		fmt.Sprintf("session-%s.tape.json", time.Now().Format("20060102-150405")))
}

// handleTapeCommand backs /tape.
func (m *Model) handleTapeCommand(parts []string) tea.Cmd {
	sub := "status"
	if len(parts) > 1 {
		sub = strings.ToLower(parts[1])
	}
	switch sub {
	case "start", "record":
		path := defaultTapePath()
		if len(parts) > 2 && strings.TrimSpace(parts[2]) != "" {
			path = parts[2]
		}
		if err := os.Setenv("GGCODE_TOOL_TAPE", "record:"+path); err != nil {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tape: failed to set env: %v", err))
			break
		}
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf(
			"Tool tape RECORD armed: %s\nRuns /restart to take effect (the tape mode is read once at agent startup). After restart, every tool call is recorded to the tape.", path))
	case "replay":
		if len(parts) < 3 || strings.TrimSpace(parts[2]) == "" {
			m.chatWriteSystem(nextSystemID(), "Usage: /tape replay <path-to-tape.json>")
			break
		}
		path := parts[2]
		if _, err := os.Stat(path); err != nil {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tape: cannot read %s: %v", path, err))
			break
		}
		if err := os.Setenv("GGCODE_TOOL_TAPE", "replay:"+path); err != nil {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tape: failed to set env: %v", err))
			break
		}
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf(
			"Tool tape REPLAY armed: %s\nRuns /restart to take effect. After restart, tool results are served from the tape (no real side effects); a tape miss is an explicit error.", path))
	case "stop", "off":
		if err := os.Setenv("GGCODE_TOOL_TAPE", ""); err != nil {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tape: failed to clear env: %v", err))
			break
		}
		m.chatWriteSystem(nextSystemID(), "Tool tape disarmed. Runs /restart to take effect; the current session keeps its live tape state.")
	case "status":
		m.renderTapeStatus()
	default:
		m.chatWriteSystem(nextSystemID(),
			"Usage: /tape start [path] | replay <path> | stop | status")
	}
	return nil
}

// renderTapeStatus renders the live tape state plus any pending env change.
func (m *Model) renderTapeStatus() {
	mode, path, entries := "off", "", 0
	if m.agent != nil {
		mode, path, entries = m.agent.ToolTapeStatus()
	}
	if mode == "record" || mode == "replay" {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf(
			"Tool tape: %s mode, %d entries, file: %s", mode, entries, path))
	} else {
		m.chatWriteSystem(nextSystemID(), "Tool tape: off")
	}
	if pending := strings.TrimSpace(os.Getenv("GGCODE_TOOL_TAPE")); pending != "" {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf(
			"Pending for next /restart: GGCODE_TOOL_TAPE=%s", pending))
	}
}
