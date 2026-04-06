package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/topcheer/ggcode/internal/commands"
)

type skillsPanelState struct {
	page int
}

const skillsPerPage = 10

func (m *Model) openSkillsPanel() {
	m.refreshCommands()
	m.skillsPanel = &skillsPanelState{page: 0}
}

func (m *Model) closeSkillsPanel() {
	m.skillsPanel = nil
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
	skills = skills[start:end]

	groups := map[string][]*commands.Command{
		"Bundled skills":  {},
		"Project skills":  {},
		"User skills":     {},
		"Plugin skills":   {},
		"MCP skills":      {},
		"Legacy commands": {},
	}
	for _, skill := range skills {
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

	var body []string
	for _, title := range []string{"Bundled skills", "Project skills", "User skills", "Plugin skills", "MCP skills", "Legacy commands"} {
		group := groups[title]
		if len(group) == 0 {
			continue
		}
		body = append(body, lipgloss.NewStyle().Bold(true).Render(title))
		for _, skill := range group {
			body = append(body, fmt.Sprintf("  %s", skill.Name))
			if desc := strings.TrimSpace(skill.Description); desc != "" {
				body = append(body, fmt.Sprintf("    %s", desc))
			}
			if when := strings.TrimSpace(skill.WhenToUse); when != "" {
				body = append(body, fmt.Sprintf("    Use when: %s", when))
			}
			switch {
			case skill.UserInvocable:
				body = append(body, fmt.Sprintf("    Shortcut: %s", skill.SlashName()))
			default:
				body = append(body, "    Agent-only")
			}
		}
		body = append(body, "")
	}
	body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(
		fmt.Sprintf(" \u2190/\u2192 page %d/%d \u00b7 Esc close", page+1, pageCount),
	))
	return m.renderContextBox("/skills", strings.TrimRight(strings.Join(body, "\n"), "\n"), lipgloss.Color("12"))
}

func (m *Model) handleSkillsPanelKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "left", "h":
		if m.skillsPanel != nil && m.skillsPanel.page > 0 {
			m.skillsPanel.page--
		}
	case "right", "l":
		if m.skillsPanel != nil {
			maxPage := skillsPageCount(len(m.listSkills())) - 1
			if m.skillsPanel.page < maxPage {
				m.skillsPanel.page++
			}
		}
	case "esc", "ctrl+c":
		m.closeSkillsPanel()
	}
	return *m, nil
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
