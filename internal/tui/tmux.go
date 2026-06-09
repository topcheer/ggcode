package tui

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/tmux"
)

func detectTmuxForTUI() (*tmux.Client, *tmux.Environment) {
	client := tmux.NewClient()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	env, err := client.Detect(ctx)
	if err != nil || env == nil || !env.Available || !env.InTmux {
		return client, env
	}
	return client, env
}

func (m *Model) tmuxAvailable() bool {
	return m.tmuxClient != nil && m.tmuxEnv != nil && m.tmuxEnv.Available && m.tmuxEnv.InTmux
}

func (m *Model) tmuxLabel() string {
	if !m.tmuxAvailable() {
		return ""
	}
	return m.tmuxEnv.Label()
}

func (m *Model) tmuxWorkspace() string {
	if m.agent != nil && strings.TrimSpace(m.agent.WorkingDir()) != "" {
		return m.agent.WorkingDir()
	}
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

func (m *Model) handleTmuxCommand(parts []string) tea.Cmd {
	if len(parts) == 1 || strings.EqualFold(parts[1], "status") {
		m.chatWriteSystem(nextSystemID(), m.tmuxStatusText())
		return nil
	}
	if !m.tmuxAvailable() {
		m.chatWriteSystem(nextSystemID(), "tmux is not available in this terminal session")
		return nil
	}
	sub := strings.ToLower(parts[1])
	switch sub {
	case "split", "shell":
		cmd := strings.TrimSpace(strings.Join(parts[2:], " "))
		m.openTmuxSplit("shell", cmd, true)
	case "test":
		m.openTmuxSplit("test", "go test -tags goolm ./...", false)
	case "build":
		m.openTmuxSplit("build", "go build -tags goolm ./...", false)
	case "verify":
		m.openTmuxSplit("verify", "make verify-ci", false)
	case "popup":
		cmd := strings.TrimSpace(strings.Join(parts[2:], " "))
		m.openTmuxPopup(cmd)
	case "list":
		m.chatWriteSystem(nextSystemID(), m.tmuxManagedPaneText())
	case "refresh":
		m.refreshTmuxPanes()
	case "capture", "output":
		if len(parts) < 3 {
			m.chatWriteSystem(nextSystemID(), "usage: /tmux capture <pane-id> [lines]")
			return nil
		}
		m.captureTmuxPane(parts[2], parseTmuxLines(parts[3:], 200))
	case "close", "kill":
		if len(parts) < 3 {
			m.chatWriteSystem(nextSystemID(), "usage: /tmux close <pane-id>")
			return nil
		}
		m.closeTmuxPane(parts[2])
	case "focus":
		if len(parts) < 3 {
			m.chatWriteSystem(nextSystemID(), "usage: /tmux focus <pane-id>")
			return nil
		}
		m.focusTmuxPane(parts[2])
	default:
		m.chatWriteSystem(nextSystemID(), tmuxHelpText())
	}
	return nil
}

func (m *Model) tmuxStatusText() string {
	if m.tmuxEnv == nil {
		return "tmux: not detected"
	}
	if !m.tmuxEnv.Available {
		return "tmux: command not found"
	}
	if !m.tmuxEnv.InTmux {
		return fmt.Sprintf("tmux: available (%s), not inside a tmux session", m.tmuxEnv.Version)
	}
	return fmt.Sprintf("tmux: %s\nversion: %s\nworkspace: %s\nmanaged panes: %d", m.tmuxEnv.Label(), m.tmuxEnv.Version, m.tmuxWorkspace(), len(m.tmuxManagedPanes))
}

func (m *Model) openTmuxSplit(purpose, command string, horizontal bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pane, err := m.tmuxClient.Split(ctx, tmux.SplitRequest{
		Workspace:  m.tmuxWorkspace(),
		Command:    command,
		Purpose:    purpose,
		Horizontal: horizontal,
		Size:       "35%",
	})
	if err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux split failed: %v", err))
		return
	}
	if m.tmuxManagedPanes == nil {
		m.tmuxManagedPanes = make(map[string]tmux.Pane)
	}
	m.tmuxManagedPanes[pane.ID] = *pane
	m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux pane created: %s (%s)", pane.ID, purpose))
}

func (m *Model) openTmuxPopup(command string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := m.tmuxClient.Popup(ctx, tmux.PopupRequest{Workspace: m.tmuxWorkspace(), Command: command}); err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux popup failed: %v", err))
		return
	}
	m.chatWriteSystem(nextSystemID(), "tmux popup opened")
}

func (m *Model) focusTmuxPane(paneID string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := m.tmuxClient.Focus(ctx, paneID); err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux focus failed: %v", err))
		return
	}
	m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux focused pane: %s", paneID))
}

func (m *Model) captureTmuxPane(paneID string, lines int) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := m.tmuxClient.Capture(ctx, paneID, lines)
	if err != nil {
		m.markTmuxPaneAlive(paneID, false)
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux capture failed: %v", err))
		return
	}
	out = strings.TrimRight(out, "\n")
	if out == "" {
		out = "(no output)"
	}
	m.markTmuxPaneAlive(paneID, true)
	m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux capture %s (last %d lines):\n%s", paneID, lines, out))
}

func (m *Model) closeTmuxPane(paneID string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := m.tmuxClient.KillPane(ctx, paneID); err != nil {
		m.markTmuxPaneAlive(paneID, false)
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux close failed: %v", err))
		return
	}
	delete(m.tmuxManagedPanes, paneID)
	m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux pane closed: %s", paneID))
}

func (m *Model) refreshTmuxPanes() {
	if len(m.tmuxManagedPanes) == 0 {
		m.chatWriteSystem(nextSystemID(), "tmux managed panes: none")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	aliveIDs, err := m.tmuxClient.ListPaneIDs(ctx)
	if err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux refresh failed: %v", err))
		return
	}
	alive := 0
	stale := 0
	for id, pane := range m.tmuxManagedPanes {
		_, ok := aliveIDs[id]
		pane.Alive = ok
		m.tmuxManagedPanes[id] = pane
		if ok {
			alive++
		} else {
			stale++
		}
	}
	m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux panes refreshed: %d alive, %d stale\n%s", alive, stale, m.tmuxManagedPaneText()))
}

func (m *Model) markTmuxPaneAlive(paneID string, alive bool) {
	if pane, ok := m.tmuxManagedPanes[paneID]; ok {
		pane.Alive = alive
		m.tmuxManagedPanes[paneID] = pane
	}
}

func parseTmuxLines(args []string, fallback int) int {
	if len(args) == 0 {
		return fallback
	}
	lines, err := strconv.Atoi(args[0])
	if err != nil || lines <= 0 {
		return fallback
	}
	return lines
}

func (m *Model) tmuxManagedPaneText() string {
	if len(m.tmuxManagedPanes) == 0 {
		return "tmux managed panes: none"
	}
	var b strings.Builder
	b.WriteString("tmux managed panes:\n")
	for _, pane := range m.tmuxManagedPanes {
		state := "stale"
		if pane.Alive {
			state = "alive"
		}
		b.WriteString(fmt.Sprintf("- %s [%s/%s] %s\n", pane.ID, pane.Purpose, state, pane.Command))
	}
	return strings.TrimSpace(b.String())
}

func tmuxHelpText() string {
	return strings.TrimSpace(`tmux commands:
/tmux status
/tmux split [cmd]
/tmux test
/tmux build
/tmux verify
/tmux popup [cmd]
/tmux list
/tmux refresh
/tmux capture <pane-id> [lines]
/tmux close <pane-id>
/tmux focus <pane-id>

Shortcut: Ctrl+X opens the tmux action menu when running inside tmux.`)
}

func (m *Model) openTmuxMenu() {
	if !m.tmuxAvailable() {
		m.chatWriteSystem(nextSystemID(), "tmux is not available in this terminal session")
		return
	}
	m.tmuxMenuOpen = true
	m.statusActivity = "tmux menu"
}

func (m Model) handleTmuxMenuKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "ctrl+c", "q":
		m.tmuxMenuOpen = false
		m.statusActivity = ""
	case "s":
		m.tmuxMenuOpen = false
		m.openTmuxSplit("shell", "", true)
	case "v":
		m.tmuxMenuOpen = false
		m.openTmuxSplit("shell", "", false)
	case "t":
		m.tmuxMenuOpen = false
		m.openTmuxSplit("test", "go test -tags goolm ./...", false)
	case "b":
		m.tmuxMenuOpen = false
		m.openTmuxSplit("build", "go build -tags goolm ./...", false)
	case "d":
		m.tmuxMenuOpen = false
		m.openTmuxSplit("dev", "make dev", true)
	case "p":
		m.tmuxMenuOpen = false
		m.openTmuxPopup("")
	case "l":
		m.chatWriteSystem(nextSystemID(), m.tmuxManagedPaneText())
	case "r":
		m.refreshTmuxPanes()
	case "?", "h":
		m.chatWriteSystem(nextSystemID(), tmuxHelpText())
	}
	return m, nil
}

func (m Model) renderTmuxMenu() string {
	if !m.tmuxMenuOpen {
		return ""
	}
	return "tmux actions\n" +
		"────────────────\n" +
		"s  shell pane right\n" +
		"v  shell pane bottom\n" +
		"t  run tests bottom\n" +
		"b  run build bottom\n" +
		"d  dev server pane\n" +
		"p  popup shell\n" +
		"l  list managed panes\n" +
		"r  refresh pane status\n" +
		"?  help\n" +
		"Esc/q close"
}
