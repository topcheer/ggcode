package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/commands"
)

type skillsPanelState struct {
	page   int
	cursor int // index within page; indexes draft skills first, then live skills

	status    string // last activation/toggle feedback line
	statusErr bool
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
	if runes := []rune(desc); len(runes) > 80 {
		// Try to truncate at last space before 80 runes
		truncStr := string(runes[:80])
		if byteIdx := strings.LastIndexByte(truncStr, ' '); byteIdx > 40 {
			// byteIdx is a byte offset in truncStr; convert to rune count
			runeIdx := utf8.RuneCountInString(truncStr[:byteIdx])
			desc = string(runes[:runeIdx]) + "..."
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
	drafts := m.listSkillDrafts()
	if len(skills) == 0 && len(drafts) == 0 {
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
	draftCount := len(drafts)
	var body []string
	// Draft skills surface first: auto-distilled drafts are invisible to the
	// live registry by design (human review gate), so the panel is their only
	// discovery surface. Cursor indexes drafts before live skills.
	if draftCount > 0 {
		body = append(body, lipgloss.NewStyle().Bold(true).Render("Draft skills (pending review)"))
		for i, draft := range drafts {
			prefix := "  "
			if i == cursorIdx {
				prefix = "▸ "
			}
			line := fmt.Sprintf("%s◌ %s", prefix, draft.Name)
			if i == cursorIdx {
				line = lipgloss.NewStyle().Bold(true).Render(line)
			}
			body = append(body, line)
			if desc := summarizeDescription(draft.Description); desc != "" {
				body = append(body, fmt.Sprintf("    %s", lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(desc)))
			}
		}
		body = append(body, "")
	}
	flatIdx := draftCount // track index within page for cursor (after drafts)
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

	// Status line from the last activation attempt
	if m.skillsPanel.status != "" {
		color := lipgloss.Color("2")
		if m.skillsPanel.statusErr {
			color = lipgloss.Color("1")
		}
		body = append(body, lipgloss.NewStyle().Foreground(color).Render(" "+m.skillsPanel.status))
	}

	// Footer with navigation hints
	hints := []string{"←/→ page", "↑/↓ select", "Space toggle", "d toggle all"}
	if draftCount > 0 {
		hints = append(hints, "a activate draft")
	}
	hints = append(hints, "Esc close")
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
			max := len(m.listSkillDrafts()) + (end - start) - 1
			if m.skillsPanel.cursor < max {
				m.skillsPanel.cursor++
			}
		}
	case " ", "space":
		// Toggle enabled/disabled for selected live skill; drafts activate via 'a'
		if m.skillsPanel != nil {
			if m.skillsPanel.cursor < len(m.listSkillDrafts()) {
				m.skillsPanel.status = "draft skill: press a to activate"
				m.skillsPanel.statusErr = false
			} else {
				m.toggleSelectedSkill()
			}
		}
	case "a":
		// Activate the selected draft skill (move into .ggcode/skills/)
		if m.skillsPanel != nil {
			m.activateSelectedSkillDraft()
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

// groupedSkillsFlat returns the page skills flattened in RENDER order
// (Bundled, Project, User, Plugin, MCP, Legacy), the single source shared
// by the panel highlight loop and toggleSelectedSkill (#911).
func groupedSkillsFlat(pageSkills []*commands.Command) []*commands.Command {
	groups := map[string][]*commands.Command{
		"Bundled skills": {}, "Project skills": {}, "User skills": {},
		"Plugin skills": {}, "MCP skills": {}, "Legacy commands": {},
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
	flat := make([]*commands.Command, 0, len(pageSkills))
	for _, title := range []string{"Bundled skills", "Project skills", "User skills", "Plugin skills", "MCP skills", "Legacy commands"} {
		flat = append(flat, groups[title]...)
	}
	return flat
}

// toggleSelectedSkill toggles the Enabled state of the currently selected skill.
// Builtin skills cannot be disabled.
func (m *Model) toggleSelectedSkill() {
	if m.skillsPanel == nil || m.commandMgr == nil {
		return
	}
	// #911: the cursor indexes the RENDER order (drafts first, then grouped
	// live skills), so subtract the draft section before indexing live skills.
	draftCount := len(m.listSkillDrafts())
	if m.skillsPanel.cursor < draftCount {
		return // cursor on a draft; toggling is not applicable
	}
	skills := m.listSkills()
	pageCount := skillsPageCount(len(skills))
	page := clampSkillsPage(m.skillsPanel.page, pageCount)
	start := page * skillsPerPage
	end := start + skillsPerPage
	if end > len(skills) {
		end = len(skills)
	}
	flat := groupedSkillsFlat(skills[start:end])
	liveCursor := m.skillsPanel.cursor - draftCount
	if liveCursor < 0 || liveCursor >= len(flat) {
		return
	}
	skill := flat[liveCursor]
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
	// #1392-A: the system prompt is a snapshot cached in the context
	// (agent.UpdateSystemPrompt swaps the first system message) - the
	// single-toggle path calls rebuildSystemPrompt but this bulk path
	// never did, so 'D' left every disabled skill's description in the
	// prompt until a single toggle or a session restart.
	m.rebuildSystemPrompt()
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

// listSkillDrafts returns pending draft skills from the project's
// skill-drafts directory (empty when commandMgr or the dir is absent).
func (m *Model) listSkillDrafts() []commands.SkillDraft {
	if m.commandMgr == nil {
		return nil
	}
	return commands.ListSkillDrafts(m.commandMgr.ProjectDir())
}

// activateSelectedSkillDraft moves the selected draft into the live project
// registry (.ggcode/skills/<name>/SKILL.md) and refreshes the panel and the
// system prompt so the reviewed skill is immediately usable.
func (m *Model) activateSelectedSkillDraft() {
	if m.skillsPanel == nil || m.commandMgr == nil {
		return
	}
	drafts := m.listSkillDrafts()
	if m.skillsPanel.cursor < 0 || m.skillsPanel.cursor >= len(drafts) {
		return
	}
	draft := drafts[m.skillsPanel.cursor]
	name, err := commands.ActivateSkillDraft(m.commandMgr.ProjectDir(), draft.Path)
	if err != nil {
		m.skillsPanel.status = fmt.Sprintf("activate %s: %v", draft.Name, err)
		m.skillsPanel.statusErr = true
		return
	}
	m.commandMgr.Reload() // pick up the activated skill immediately (no watcher dependency)
	m.refreshCommands()
	m.rebuildSystemPrompt()
	m.skillsPanel.status = fmt.Sprintf("activated %s → .ggcode/skills/%s", name, name)
	m.skillsPanel.statusErr = false
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
