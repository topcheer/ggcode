package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/session"
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

func (m *Model) tmuxManagerForWorkspace() *tmux.Manager {
	if m.tmuxManager == nil {
		m.tmuxManager = tmux.SharedManager(m.tmuxWorkspace())
	}
	return m.tmuxManager
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
	if len(parts) > 1 {
		sub := strings.ToLower(parts[1])
		if sub == "enter" || sub == "attach" || sub == "new" {
			sessionName, setupLayout := parseTmuxEnterArgs(parts[2:])
			return m.enterTmuxSession(sessionName, setupLayout)
		}
	}
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
	case "logs", "tail-all":
		m.showTmuxLogs(parseTmuxLines(parts[2:], 50))
	case "layouts":
		m.chatWriteSystem(nextSystemID(), m.tmuxLayoutsText())
	case "layout":
		name := ""
		if len(parts) > 2 {
			name = parts[2]
		}
		m.chatWriteSystem(nextSystemID(), m.tmuxLayoutText(name))
	case "setup":
		name := ""
		if len(parts) > 2 {
			name = parts[2]
		}
		m.setupTmuxLayout(name)
	case "save-layout", "savelayout":
		name := ""
		if len(parts) > 2 {
			name = parts[2]
		}
		m.saveTmuxLayout(name)
	case "delete-layout", "rm-layout":
		name := ""
		if len(parts) > 2 {
			name = parts[2]
		}
		m.deleteTmuxLayout(name)
	case "rename-layout":
		if len(parts) < 4 {
			m.chatWriteSystem(nextSystemID(), "usage: /tmux rename-layout <old> <new>")
			return nil
		}
		m.renameTmuxLayout(parts[2], parts[3])
	case "refresh":
		m.refreshTmuxPanes()
	case "restore":
		selector := ""
		if len(parts) > 2 {
			selector = parts[2]
		}
		m.restoreTmuxPanes(selector)
	case "rerun":
		if len(parts) < 3 {
			m.chatWriteSystem(nextSystemID(), "usage: /tmux rerun <pane-id|purpose>")
			return nil
		}
		m.rerunTmuxPane(parts[2])
	case "prune":
		selector := ""
		if len(parts) > 2 {
			selector = parts[2]
		}
		m.pruneTmuxPanes(selector)
	case "capture", "output":
		if len(parts) < 3 {
			m.chatWriteSystem(nextSystemID(), "usage: /tmux capture <pane-id|purpose> [lines]")
			return nil
		}
		m.captureTmuxPane(parts[2], parseTmuxLines(parts[3:], 200))
	case "close", "kill":
		if len(parts) < 3 {
			m.chatWriteSystem(nextSystemID(), "usage: /tmux close <pane-id|purpose>")
			return nil
		}
		m.closeTmuxPane(parts[2])
	case "stop":
		if len(parts) < 3 {
			m.chatWriteSystem(nextSystemID(), "usage: /tmux stop <pane-id|purpose>")
			return nil
		}
		m.stopTmuxPane(parts[2])
	case "focus":
		if len(parts) < 3 {
			m.chatWriteSystem(nextSystemID(), "usage: /tmux focus <pane-id|purpose>")
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
	return fmt.Sprintf("tmux: %s\nversion: %s\nworkspace: %s\nmanaged panes: %d", m.tmuxEnv.Label(), m.tmuxEnv.Version, m.tmuxWorkspace(), m.tmuxManagerForWorkspace().Count())
}

func (m *Model) enterTmuxSession(sessionName, setupLayout string) tea.Cmd {
	if m.tmuxClient == nil {
		m.tmuxClient = tmux.NewClient()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	env, err := m.tmuxClient.Detect(ctx)
	if err != nil || env == nil || !env.Available {
		m.chatWriteSystem(nextSystemID(), "tmux: command not found")
		return nil
	}
	m.tmuxEnv = env
	if env.InTmux {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("already inside tmux: %s", env.Label()))
		return nil
	}
	if m.session == nil || strings.TrimSpace(m.session.ID) == "" {
		m.chatWriteSystem(nextSystemID(), "tmux enter requires an active session to resume")
		return nil
	}
	if sessionName = sanitizeTmuxSessionName(sessionName); sessionName == "" {
		sessionName = defaultTmuxSessionName(m.tmuxWorkspace())
	}
	if m.sessionStore != nil && m.session != nil {
		ses := m.session
		store := m.sessionStore
		// With per-message persistence (SetPersistHandler), all messages are
		// already on disk. Only flush meta for JSONLStore; full Save would
		// race with concurrent onPersist appends.
		if jsonlStore, ok := store.(*session.JSONLStore); ok {
			safego.Go("tui.tmux.metaSave", func() {
				if err := jsonlStore.AppendMetaToDisk(ses); err != nil {
					debug.Log("tui", "tmux metaSave: %v", err)
				}
			})
		} else {
			safego.Go("tui.tmux.sessionSave", func() {
				if err := store.Save(ses); err != nil {
					debug.Log("tui", "tmux sessionSave: %v", err)
				}
			})
		}
	}
	m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Entering tmux session %q and resuming session %s...", sessionName, m.session.ID))
	m.tmuxExecRequested = true
	m.tmuxExecSession = sessionName
	if strings.TrimSpace(setupLayout) != "" {
		m.tmuxExecSetupLayout = tmuxLayoutName(setupLayout)
	}
	m.quitting = true
	return tea.Quit
}

func parseTmuxEnterArgs(args []string) (sessionName, setupLayout string) {
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		switch {
		case arg == "--setup" || arg == "setup":
			setupLayout = "default"
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				setupLayout = args[i+1]
				i++
			}
		case strings.HasPrefix(arg, "--setup="):
			setupLayout = strings.TrimPrefix(arg, "--setup=")
		case sessionName == "" && arg != "":
			sessionName = arg
		}
	}
	return sessionName, setupLayout
}

func sanitizeTmuxSessionName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		case r == '.', r == ':':
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func defaultTmuxSessionName(workspace string) string {
	base := strings.TrimSpace(workspace)
	if base == "" || base == "." {
		return "ggcode"
	}
	base = strings.TrimRight(base, string(os.PathSeparator))
	idx := strings.LastIndex(base, string(os.PathSeparator))
	if idx >= 0 && idx+1 < len(base) {
		base = base[idx+1:]
	}
	base = sanitizeTmuxSessionName(base)
	if base == "" {
		return "ggcode"
	}
	if len([]rune(base)) > 48 {
		base = string([]rune(base)[:48])
		base = strings.TrimRight(base, "-")
	}
	return "ggcode-" + base
}

func (m *Model) openTmuxSplit(purpose, command string, horizontal bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pane, err := m.tmuxManagerForWorkspace().Split(ctx, tmux.SplitRequest{
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
	m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux pane created: %s (%s)", pane.ID, purpose))
}

func (m *Model) openTmuxPopup(command string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := m.tmuxManagerForWorkspace().Popup(ctx, tmux.PopupRequest{Workspace: m.tmuxWorkspace(), Command: command}); err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux popup failed: %v", err))
		return
	}
	m.chatWriteSystem(nextSystemID(), "tmux popup opened")
}

func (m *Model) focusTmuxPane(selector string) {
	pane, ok := m.tmuxManagerForWorkspace().ResolvePaneSelector(selector)
	if !ok {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux focus failed: no managed pane matches %q", selector))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := m.tmuxManagerForWorkspace().Focus(ctx, pane.ID); err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux focus failed: %v", err))
		return
	}
	m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux focused pane: %s", pane.ID))
}

func (m *Model) captureTmuxPane(selector string, lines int) {
	pane, ok := m.tmuxManagerForWorkspace().ResolvePaneSelector(selector)
	if !ok {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux capture failed: no managed pane matches %q", selector))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := m.tmuxManagerForWorkspace().Capture(ctx, pane.ID, lines)
	if err != nil {
		m.markTmuxPaneAlive(pane.ID, false)
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux capture failed: %v", err))
		return
	}
	out = strings.TrimRight(out, "\n")
	if out == "" {
		out = "(no output)"
	}
	m.markTmuxPaneAlive(pane.ID, true)
	m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux capture %s (last %d lines):\n%s", pane.ID, lines, out))
}

func (m *Model) stopTmuxPane(selector string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	pane, err := m.tmuxManagerForWorkspace().StopPane(ctx, selector)
	if err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux stop failed: %v", err))
		return
	}
	m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux pane stopped: %s [%s] (metadata kept)", pane.ID, pane.Purpose))
}

func (m *Model) closeTmuxPane(selector string) {
	pane, ok := m.tmuxManagerForWorkspace().ResolvePaneSelector(selector)
	if !ok {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux close failed: no managed pane matches %q", selector))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := m.tmuxManagerForWorkspace().Close(ctx, pane.ID); err != nil {
		m.markTmuxPaneAlive(pane.ID, false)
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux close failed: %v", err))
		return
	}
	m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux pane closed: %s", pane.ID))
}

func (m *Model) deleteTmuxLayout(name string) {
	layoutName := tmuxLayoutName(name)
	if !m.tmuxManagerForWorkspace().DeleteLayout(layoutName) {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux layout %q not found", layoutName))
		return
	}
	m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux layout %q deleted", layoutName))
}

func (m *Model) renameTmuxLayout(oldName, newName string) {
	oldLayout := tmuxLayoutName(oldName)
	newLayout := tmuxLayoutName(newName)
	if err := m.tmuxManagerForWorkspace().RenameLayout(oldLayout, newLayout); err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux rename-layout failed: %v", err))
		return
	}
	m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux layout %q renamed to %q", oldLayout, newLayout))
}

func (m *Model) refreshTmuxPanes() {
	if !m.tmuxManagerForWorkspace().HasPanes() {
		m.chatWriteSystem(nextSystemID(), "tmux managed panes: none")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	alive, stale, err := m.tmuxManagerForWorkspace().Refresh(ctx)
	if err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux refresh failed: %v", err))
		return
	}
	m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux panes refreshed: %d alive, %d stale\n%s", alive, stale, m.tmuxManagedPaneText()))
}

func (m *Model) restoreTmuxPanes(selector string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	results, err := m.tmuxManagerForWorkspace().Restore(ctx, selector)
	if err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux restore failed: %v", err))
		return
	}
	if len(results) == 0 {
		m.chatWriteSystem(nextSystemID(), "tmux restore: no matching stale panes with commands")
		return
	}
	var b strings.Builder
	b.WriteString("tmux restored panes:\n")
	for _, res := range results {
		b.WriteString(fmt.Sprintf("- %s -> %s [%s] %s\n", res.Old.ID, res.New.ID, res.New.Purpose, res.New.Command))
	}
	m.chatWriteSystem(nextSystemID(), strings.TrimSpace(b.String()))
}

func (m *Model) rerunTmuxPane(selector string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	res, err := m.tmuxManagerForWorkspace().RerunPane(ctx, selector)
	if err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux rerun failed: %v", err))
		return
	}
	m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux reran pane: %s -> %s [%s] %s", res.Old.ID, res.New.ID, res.New.Purpose, res.New.Command))
}

func (m *Model) pruneTmuxPanes(selector string) {
	removed := m.tmuxManagerForWorkspace().Prune(selector)
	if removed == 0 {
		m.chatWriteSystem(nextSystemID(), "tmux prune: no matching stale panes")
		return
	}
	m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux pruned %d stale pane(s)\n%s", removed, m.tmuxManagedPaneText()))
}

func (m *Model) saveTmuxLayout(name string) {
	if err := m.tmuxManagerForWorkspace().SaveLayout(name); err != nil {
		if errors.Is(err, tmux.ErrNoAlivePanes) {
			m.chatWriteSystem(nextSystemID(), "tmux save-layout: no alive panes to save")
			return
		}
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux save-layout failed: %v", err))
		return
	}
	m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux layout %q saved", tmuxLayoutName(name)))
}

func (m *Model) setupTmuxLayout(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	created, err := m.tmuxManagerForWorkspace().SetupLayout(ctx, name)
	if err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux setup failed: %v", err))
		return
	}
	if len(created) == 0 {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("tmux setup %q: no missing panes", tmuxLayoutName(name)))
		return
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("tmux setup %q created panes:\n", tmuxLayoutName(name)))
	for _, pane := range created {
		b.WriteString(fmt.Sprintf("- %s [%s] %s\n", pane.ID, pane.Purpose, pane.Command))
	}
	m.chatWriteSystem(nextSystemID(), strings.TrimSpace(b.String()))
}

func (m *Model) showTmuxLogs(lines int) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	captures := m.tmuxManagerForWorkspace().CaptureAll(ctx, lines)
	m.chatWriteSystem(nextSystemID(), tmux.FormatCaptures(captures, lines))
}

func (m *Model) tmuxLayoutsText() string {
	names := m.tmuxManagerForWorkspace().ListLayoutNames()
	if len(names) == 0 {
		return "tmux layouts: none"
	}
	return "tmux layouts:\n- " + strings.Join(names, "\n- ")
}

func (m *Model) tmuxLayoutText(name string) string {
	layoutName := tmuxLayoutName(name)
	layout := m.tmuxManagerForWorkspace().Layout(layoutName)
	if len(layout) == 0 {
		return fmt.Sprintf("tmux layout %q: empty or not found", layoutName)
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("tmux layout %q:\n", layoutName))
	for _, pane := range layout {
		b.WriteString(fmt.Sprintf("- [%s] %s\n", pane.Purpose, pane.Command))
	}
	return strings.TrimSpace(b.String())
}

func tmuxLayoutName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "default"
	}
	return name
}

func (m *Model) markTmuxPaneAlive(paneID string, alive bool) {
	m.tmuxManagerForWorkspace().MarkAlive(paneID, alive)
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
	return m.tmuxManagerForWorkspace().ManagedPaneText()
}

func tmuxHelpText() string {
	return strings.TrimSpace(`tmux commands:
	/tmux status
	/tmux enter [session-name]
	/tmux enter [session-name] --setup [layout]
	/tmux split [cmd]
/tmux test
/tmux build
/tmux verify
/tmux popup [cmd]
/tmux list
/tmux logs [lines]
/tmux layouts
/tmux layout [name]
/tmux setup [name]
/tmux save-layout [name]
/tmux delete-layout [name]
/tmux rename-layout <old> <new>
/tmux refresh
/tmux restore [pane-id|purpose]
/tmux rerun <pane-id|purpose>
/tmux prune [pane-id|purpose]
/tmux capture <pane-id|purpose> [lines]
/tmux stop <pane-id|purpose>
/tmux close <pane-id|purpose>
/tmux focus <pane-id|purpose>

Shortcut: Ctrl+X opens the tmux action menu when running inside tmux.`)
}

func (m *Model) openTmuxMenu() {
	if !m.tmuxAvailable() {
		if m.tmuxClient == nil {
			m.tmuxClient = tmux.NewClient()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		env, err := m.tmuxClient.Detect(ctx)
		if err != nil || env == nil || !env.Available {
			m.chatWriteSystem(nextSystemID(), "tmux: command not found")
			return
		}
		m.tmuxEnv = env
	}
	m.tmuxMenuOpen = true
	m.statusActivity = "tmux menu"
}

func (m Model) handleTmuxMenuKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "ctrl+c", "q":
		m.tmuxMenuOpen = false
		m.statusActivity = ""
	case "enter", "e":
		if !m.tmuxAvailable() {
			m.tmuxMenuOpen = false
			m.statusActivity = ""
			return m, m.enterTmuxSession("", "")
		}
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
	case "R":
		m.restoreTmuxPanes("")
	case "S":
		m.setupTmuxLayout("")
	case "x":
		m.pruneTmuxPanes("")
	case "?", "h":
		m.chatWriteSystem(nextSystemID(), tmuxHelpText())
	}
	return m, nil
}

func (m Model) renderTmuxMenu() string {
	if !m.tmuxMenuOpen {
		return ""
	}
	if !m.tmuxAvailable() {
		return "tmux actions\n" +
			"────────────────\n" +
			"tmux is available, but ggcode is not running inside tmux.\n" +
			"Enter  start/attach tmux and resume this session\n" +
			"e      same as Enter\n" +
			"?      help\n" +
			"Esc/q  close"
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
		"R  restore stale panes\n" +
		"S  setup default layout\n" +
		"x  prune stale panes\n" +
		"?  help\n" +
		"Esc/q close"
}
