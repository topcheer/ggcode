package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/topcheer/ggcode/internal/hooks"
)

type hooksPanelState struct {
	selectedEvent int // 0-4
	selectedHook  int // index within event
	editMode      bool
	editingNew    bool
	editFields    hookEditFields
	fieldIdx      int // which field is being edited
	message       string
}

type hookEditFields struct {
	match        string
	matchMode    string // "glob" or "regex"
	hookType     string // "command" or "http"
	command      string
	url          string
	secret       string
	injectOutput bool
}

var hookEventNames = []string{
	"on_user_message",
	"pre_tool_use",
	"post_tool_use",
	"on_agent_stop",
	"on_stream_stop",
}

func (m *Model) hookEventLabels() []string {
	return []string{
		tr(m.language, "hooks.event.onUserMessage"),
		tr(m.language, "hooks.event.preToolUse"),
		tr(m.language, "hooks.event.postToolUse"),
		tr(m.language, "hooks.event.onAgentStop"),
		tr(m.language, "hooks.event.onStreamStop"),
	}
}

func (m *Model) hookEditFieldLabels() []string {
	return []string{
		tr(m.language, "hooks.field.match"),
		tr(m.language, "hooks.field.matchMode"),
		tr(m.language, "hooks.field.type"),
		tr(m.language, "hooks.field.command"),
		tr(m.language, "hooks.field.url"),
		tr(m.language, "hooks.field.secret"),
		tr(m.language, "hooks.field.inject"),
	}
}

func (m *Model) openHooksPanel() {
	m.hooksPanel = &hooksPanelState{
		editFields: hookEditFields{
			match:    "*",
			hookType: "command",
		},
	}
}

func (m *Model) closeHooksPanel() {
	m.hooksPanel = nil
}

func (m *Model) getCurrentHooks() hooks.HookConfig {
	if m.agent != nil {
		return m.agent.GetHookConfig()
	}
	return hooks.HookConfig{}
}

func (m *Model) getEventHooks(eventIdx int) []hooks.Hook {
	cfg := m.getCurrentHooks()
	switch eventIdx {
	case 0:
		return cfg.OnUserMessage
	case 1:
		return cfg.PreToolUse
	case 2:
		return cfg.PostToolUse
	case 3:
		return cfg.OnAgentStop
	case 4:
		return cfg.OnStreamStop
	}
	return nil
}

func (m *Model) saveHooksConfig(cfg hooks.HookConfig) {
	if m.agent != nil {
		m.agent.SetHookConfig(cfg)
	}
	if m.config != nil {
		m.config.Hooks = cfg
		_ = m.config.Save()
	}
}

func (m *Model) setEventHooks(eventIdx int, hooksList []hooks.Hook) {
	cfg := m.getCurrentHooks()
	switch eventIdx {
	case 0:
		cfg.OnUserMessage = hooksList
	case 1:
		cfg.PreToolUse = hooksList
	case 2:
		cfg.PostToolUse = hooksList
	case 3:
		cfg.OnAgentStop = hooksList
	case 4:
		cfg.OnStreamStop = hooksList
	}
	m.saveHooksConfig(cfg)
}

func (m Model) renderHooksPanel() string {
	if m.hooksPanel == nil {
		return ""
	}
	p := m.hooksPanel

	if p.editMode {
		return m.renderHooksEditForm()
	}

	width := m.viewWidth() - 4
	if width < 60 {
		width = 60
	}

	var sb strings.Builder

	// Title
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("13")).Render(" Hooks Configuration ")
	sb.WriteString(title)
	sb.WriteString("\n\n")

	// Left column: events
	leftWidth := 22
	rightWidth := width - leftWidth - 3

	// Event list
	for i, label := range m.hookEventLabels() {
		hooksList := m.getEventHooks(i)
		count := len(hooksList)
		marker := "  "
		if i == p.selectedEvent {
			marker = "▶ "
		}
		line := fmt.Sprintf("%s%-20s (%d)", marker, label, count)
		if i == p.selectedEvent {
			line = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14")).Render(line)
		}
		sb.WriteString(line)
		sb.WriteString("\n")
	}

	sb.WriteString("\n")

	// Right column: hooks for selected event
	hooksList := m.getEventHooks(p.selectedEvent)
	if len(hooksList) == 0 {
		sb.WriteString("  " + tr(m.language, "hooks.noHooks") + "\n")
	} else {
		for i, h := range hooksList {
			marker := "  "
			if i == p.selectedHook {
				marker = "▶ "
			}
			hookType := h.HasType()
			detail := ""
			switch hookType {
			case hooks.HookTypeHTTP:
				detail = h.URL
			default:
				detail = h.Command
			}
			inject := ""
			if h.InjectOutput {
				inject = " [inject]"
			}
			modeLabel := ""
			if h.MatchMode == "regex" {
				modeLabel = "(regex)"
			}
			line := fmt.Sprintf("%s[%d] %s | match%s=%s%s\n     %s", marker, i, hookType, modeLabel, h.Match, inject, detail)
			if i == p.selectedHook {
				line = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render(line)
			}
			sb.WriteString(line)
			sb.WriteString("\n")
		}
	}

	// Footer
	sb.WriteString("\n")
	footer := fmt.Sprintf(" [a]dd  [d]elete  [e]dit  [Enter]toggle  ↑↓ select event  ←→ select hook  [Esc] close")
	sb.WriteString(footer)

	if p.message != "" {
		sb.WriteString("\n\n ")
		sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(p.message))
	}

	_ = rightWidth // reserved for future use
	return sb.String()
}

func (m Model) renderHooksEditForm() string {
	p := m.hooksPanel
	f := p.editFields

	var sb strings.Builder
	title := tr(m.language, "hooks.addTitle")
	if !p.editingNew {
		title = tr(m.language, "hooks.editTitle")
	}
	sb.WriteString(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("13")).Render(title))
	sb.WriteString("\n\n")
	eventLabels := m.hookEventLabels()
	sb.WriteString(fmt.Sprintf("%s: %s\n\n", tr(m.language, "hooks.events"), eventLabels[p.selectedEvent]))

	values := []string{f.match, f.matchMode, f.hookType, f.command, f.url, f.secret, fmt.Sprintf("%v", f.injectOutput)}
	for i, label := range m.hookEditFieldLabels() {
		marker := "  "
		if i == p.fieldIdx {
			marker = "▶ "
		}
		val := values[i]
		if val == "" {
			val = "(empty)"
		}
		line := fmt.Sprintf("%s%-28s %s", marker, label, val)
		if i == p.fieldIdx {
			line = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14")).Render(line)
		}
		sb.WriteString(line)
		sb.WriteString("\n")
	}

	sb.WriteString(tr(m.language, "hooks.editHelp"))
	return sb.String()
}

func (m *Model) handleHooksPanelKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	p := m.hooksPanel

	if p.editMode {
		return m.handleHooksEditKey(msg)
	}

	switch msg.String() {
	case "esc", "q":
		m.closeHooksPanel()

	case "up":
		if p.selectedEvent > 0 {
			p.selectedEvent--
			p.selectedHook = 0
		}

	case "down":
		if p.selectedEvent < len(hookEventNames)-1 {
			p.selectedEvent++
			p.selectedHook = 0
		}

	case "left":
		if p.selectedHook > 0 {
			p.selectedHook--
		}

	case "right":
		hooksList := m.getEventHooks(p.selectedEvent)
		if p.selectedHook < len(hooksList)-1 {
			p.selectedHook++
		}

	case "a":
		p.editMode = true
		p.editingNew = true
		p.editFields = hookEditFields{match: "*", matchMode: "glob", hookType: "command"}
		p.fieldIdx = 0

	case "e":
		hooksList := m.getEventHooks(p.selectedEvent)
		if p.selectedHook >= len(hooksList) {
			p.message = tr(m.language, "hooks.msg.noSelect")
			break
		}
		h := hooksList[p.selectedHook]
		p.editMode = true
		p.editingNew = false
		p.editFields = hookEditFields{
			match:        h.Match,
			matchMode:    h.MatchMode,
			hookType:     string(h.HasType()),
			command:      h.Command,
			url:          h.URL,
			secret:       h.Secret,
			injectOutput: h.InjectOutput,
		}
		p.fieldIdx = 0

	case "d":
		hooksList := m.getEventHooks(p.selectedEvent)
		if p.selectedHook >= len(hooksList) {
			p.message = tr(m.language, "hooks.msg.noSelect")
			break
		}
		hooksList = append(hooksList[:p.selectedHook], hooksList[p.selectedHook+1:]...)
		m.setEventHooks(p.selectedEvent, hooksList)
		if p.selectedHook >= len(hooksList) && p.selectedHook > 0 {
			p.selectedHook--
		}
		p.message = tr(m.language, "hooks.msg.deleted")

	case "enter":
		// toggle inject_output on selected hook
		hooksList := m.getEventHooks(p.selectedEvent)
		if p.selectedHook < len(hooksList) {
			hooksList[p.selectedHook].InjectOutput = !hooksList[p.selectedHook].InjectOutput
			m.setEventHooks(p.selectedEvent, hooksList)
			p.message = tr(m.language, "hooks.msg.toggled")
		}
	}

	return *m, nil
}

func (m *Model) handleHooksEditKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	p := m.hooksPanel
	f := &p.editFields

	switch msg.String() {
	case "esc":
		p.editMode = false
		p.message = tr(m.language, "hooks.msg.cancelled")

	case "tab":
		p.fieldIdx++
		if p.fieldIdx >= len(m.hookEditFieldLabels()) {
			p.fieldIdx = 0
		}

	case "enter":
		// Save hook
		h := hooks.Hook{
			Match:        f.match,
			MatchMode:    f.matchMode,
			Type:         hooks.HookType(f.hookType),
			Command:      f.command,
			URL:          f.url,
			Secret:       f.secret,
			InjectOutput: f.injectOutput,
		}

		hooksList := m.getEventHooks(p.selectedEvent)
		if p.editingNew {
			hooksList = append(hooksList, h)
			p.selectedHook = len(hooksList) - 1
			p.message = tr(m.language, "hooks.msg.added")
		} else {
			if p.selectedHook < len(hooksList) {
				hooksList[p.selectedHook] = h
				p.message = tr(m.language, "hooks.msg.updated")
			}
		}
		m.setEventHooks(p.selectedEvent, hooksList)
		p.editMode = false

	case "backspace":
		m.deleteHookEditChar()

	default:
		// Regular character input
		if len(msg.String()) == 1 && msg.String()[0] >= 32 {
			m.appendHookEditChar(msg.String())
		}
	}

	return *m, nil
}

func (m *Model) appendHookEditChar(ch string) {
	p := m.hooksPanel
	f := &p.editFields
	switch p.fieldIdx {
	case 0:
		f.match += ch
	case 1:
		f.matchMode += ch
	case 2:
		f.hookType += ch
	case 3:
		f.command += ch
	case 4:
		f.url += ch
	case 5:
		f.secret += ch
	case 6:
		// toggle true/false on any key
		f.injectOutput = !f.injectOutput
	}
}

func (m *Model) deleteHookEditChar() {
	p := m.hooksPanel
	f := &p.editFields
	switch p.fieldIdx {
	case 0:
		if len(f.match) > 0 {
			f.match = f.match[:len(f.match)-1]
		}
	case 1:
		if len(f.matchMode) > 0 {
			f.matchMode = f.matchMode[:len(f.matchMode)-1]
		}
	case 2:
		if len(f.hookType) > 0 {
			f.hookType = f.hookType[:len(f.hookType)-1]
		}
	case 3:
		if len(f.command) > 0 {
			f.command = f.command[:len(f.command)-1]
		}
	case 4:
		if len(f.url) > 0 {
			f.url = f.url[:len(f.url)-1]
		}
	case 5:
		if len(f.secret) > 0 {
			f.secret = f.secret[:len(f.secret)-1]
		}
	}
}
