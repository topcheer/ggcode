package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/commands"
)

type skillsPanelState struct {
	page   int
	cursor int // index within current page
}

const skillsPerPage = 10

func (m *Model) openSkillsPanel() {
	m.refreshCommands()
	m.skillsPanel = &skillsPanelState{page: 0, cursor: 0}
}

func (m *Model) closeSkillsPanel() {
	m.skillsPanel = nil
}

// summarizeDescription returns a concise one-line summary of a skill description.
// If the description is too long or unparseable into a short summary, returns empty string.
func summarizeDescription(desc string) string {
	desc = strings.TrimSpace(desc)
	if desc == "" {
		return ""
	}
	// Take only the first line
	if idx := strings.IndexByte(desc, '\n'); idx >= 0 {
		desc = desc[:idx]
	}
	// Take only the first sentence
	if idx := strings.IndexByte(desc, '.'); idx >= 0 && idx < len(desc)-1 {
		desc = desc[:idx+1]
	}
	desc = strings.TrimSpace(desc)
	// If still too long (>80 chars), don't show it
	if len(desc) > 80 {
		// Try to truncate at last space before 80
		if idx := strings.LastIndexByte(desc[:80], ' '); idx > 40 {
			desc = desc[:idx] + "..."
		} else {
			return ""
		}
	}
	return desc
}

func (m Model) renderSkillsPanel() string {
	if m.skillsPanel == nil {
		return ""
	}
	skills := m.listSkills()
	if len(skills) == 0 {
		body := " No skills found.\n Create skills in .ggcode/skills/, ~/.ggcode/skills/, or ~/.agents/skills/."
		return m.renderContextBox("/skills", body, lipgloss.Color("12"))
	}
	pageCount := skillsPageCount(len(skills))
	page := 0
	if m.skillsPanel != nil {
		page = clampSkillsPage(m.skillsPanel.page, pageCount)
	}
	start := page * skillsPerPage
	end := start + skillsPerPage
	if end > len(skills) {
		end = len(skills)
	}
	pageSkills := skills[start:end]

	groups := map[string][]*commands.Command{
		"Bundled skills":  {},
		"Project skills":  {},
		"User skills":     {},
		"Plugin skills":   {},
		"MCP skills":      {},
		"Legacy commands": {},
	}
	for _, skill := range pageSkills {
		switch {
		case skill.LoadedFrom == commands.LoadedFromBundled || skill.Source == commands.SourceBundled:
			groups["Bundled skills"] = append(groups["Bundled skills"], skill)
		case skill.LoadedFrom == commands.LoadedFromPlugin || skill.Source == commands.SourcePlugin:
			groups["Plugin skills"] = append(groups["Plugin skills"], skill)
		case skill.LoadedFrom == commands.LoadedFromMCP || skill.Source == commands.SourceMCP:
			groups["MCP skills"] = append(groups["MCP skills"], skill)
		case skill.LoadedFrom == commands.LoadedFromCommands:
			groups["Legacy commands"] = append(groups["Legacy commands"], skill)
		case skill.Source == commands.SourceProject:
			groups["Project skills"] = append(groups["Project skills"], skill)
		default:
			groups["User skills"] = append(groups["User skills"], skill)
		}
	}

	cursorIdx := m.skillsPanel.cursor
	var body []string
	flatIdx := 0 // track index within page for cursor
	for _, title := range []string{"Bundled skills", "Project skills", "User skills", "Plugin skills", "MCP skills", "Legacy commands"} {
		group := groups[title]
		if len(group) == 0 {
			continue
		}
		body = append(body, lipgloss.NewStyle().Bold(true).Render(title))
		for _, skill := range group {
			selected := flatIdx == cursorIdx
			prefix := "  "
			if selected {
				prefix = "▸ "
			}

			name := skill.Name
			var line string
			if skill.IsBuiltin() {
				// Builtin: always on, not toggleable
				line = fmt.Sprintf("%s● %s", prefix, name)
			} else {
				// Toggleable skill
				status := "✓"
				statusColor := lipgloss.Color("2") // green
				if !skill.Enabled {
					status = "✗"
					statusColor = lipgloss.Color("1") // red
				}
				statusLabel := lipgloss.NewStyle().Foreground(statusColor).Render(status)
				if !skill.Enabled {
					name = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(name)
				}
				line = fmt.Sprintf("%s%s %s", prefix, statusLabel, name)
			}
			body = append(body, line)

			// Concise description (only if available and skill is enabled)
			if desc := summarizeDescription(skill.Description); desc != "" && skill.Enabled {
				body = append(body, fmt.Sprintf("    %s", lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(desc)))
			}

			flatIdx++
		}
		body = append(body, "")
	}

	// Footer with navigation hints
	hints := []string{"←/→ page", "↑/↓ select", "Space toggle", "d toggle all", "Esc close"}
	body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(
		fmt.Sprintf(" %s  ·  %d/%d", strings.Join(hints, " · "), page+1, pageCount),
	))
	return m.renderContextBox("/skills", strings.TrimRight(strings.Join(body, "\n"), "\n"), lipgloss.Color("12"))
}

func (m *Model) handleSkillsPanelKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "left", "h":
		if m.skillsPanel != nil && m.skillsPanel.page > 0 {
			m.skillsPanel.page--
			m.skillsPanel.cursor = 0
		}
	case "right", "l":
		if m.skillsPanel != nil {
			maxPage := skillsPageCount(len(m.listSkills())) - 1
			if m.skillsPanel.page < maxPage {
				m.skillsPanel.page++
				m.skillsPanel.cursor = 0
			}
		}
	case "up", "k":
		if m.skillsPanel != nil && m.skillsPanel.cursor > 0 {
			m.skillsPanel.cursor--
		}
	case "down", "j":
		if m.skillsPanel != nil {
			skills := m.listSkills()
			start := m.skillsPanel.page * skillsPerPage
			end := start + skillsPerPage
			if end > len(skills) {
				end = len(skills)
			}
			max := end - start - 1
			if m.skillsPanel.cursor < max {
				m.skillsPanel.cursor++
			}
		}
	case " ", "space":
		// Toggle enabled/disabled for selected skill
		if m.skillsPanel != nil {
			m.toggleSelectedSkill()
		}
	case "d":
		// Toggle all non-builtin skills
		if m.skillsPanel != nil {
			m.toggleAllNonBuiltinSkills()
		}
	case "esc", "ctrl+c":
		m.closeSkillsPanel()
	}
	return *m, nil
}

// toggleSelectedSkill toggles the Enabled state of the currently selected skill.
// Builtin skills cannot be disabled.
func (m *Model) toggleSelectedSkill() {
	if m.skillsPanel == nil || m.commandMgr == nil {
		return
	}
	skills := m.listSkills()
	start := m.skillsPanel.page * skillsPerPage
	idx := start + m.skillsPanel.cursor
	if idx < 0 || idx >= len(skills) {
		return
	}
	skill := skills[idx]
	if skill.IsBuiltin() {
		return // cannot disable builtin skills
	}
	skill.Enabled = !skill.Enabled
	m.commandMgr.SetEnabled(skill.Name, skill.Enabled)
	m.rebuildSystemPrompt()
}

// toggleAllNonBuiltinSkills toggles all non-builtin skills.
// If any non-builtin is enabled, disables all. If all are disabled, enables all.
func (m *Model) toggleAllNonBuiltinSkills() {
	if m.commandMgr == nil {
		return
	}
	skills := m.listSkills()
	anyEnabled := false
	for _, s := range skills {
		if !s.IsBuiltin() && s.Enabled {
			anyEnabled = true
			break
		}
	}
	for _, s := range skills {
		if s.IsBuiltin() {
			continue
		}
		s.Enabled = !anyEnabled
		m.commandMgr.SetEnabled(s.Name, s.Enabled)
	}
}

func (m *Model) listSkills() []*commands.Command {
	if m.commandMgr != nil {
		return m.commandMgr.List()
	}
	if len(m.customCmds) == 0 {
		return nil
	}
	var out []*commands.Command
	for _, cmd := range m.customCmds {
		if cmd == nil {
			continue
		}
		out = append(out, cmd)
	}
	sortCommands(out)
	return out
}

func sortCommands(cmds []*commands.Command) {
	if len(cmds) < 2 {
		return
	}
	for i := 0; i < len(cmds)-1; i++ {
		for j := i + 1; j < len(cmds); j++ {
			if commandsLess(cmds[j], cmds[i]) {
				cmds[i], cmds[j] = cmds[j], cmds[i]
			}
		}
	}
}

func commandsLess(a, b *commands.Command) bool {
	if a.Source != b.Source {
		return a.Source < b.Source
	}
	if a.LoadedFrom != b.LoadedFrom {
		return a.LoadedFrom < b.LoadedFrom
	}
	return a.Name < b.Name
}

func skillsPageCount(total int) int {
	if total <= 0 {
		return 1
	}
	pages := total / skillsPerPage
	if total%skillsPerPage != 0 {
		pages++
	}
	return pages
}

func clampSkillsPage(page, pageCount int) int {
	if page < 0 {
		return 0
	}
	if pageCount <= 0 {
		return 0
	}
	if page >= pageCount {
		return pageCount - 1
	}
	return page
}
