package tui

import (
	"fmt"
	"github.com/topcheer/ggcode/internal/util"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/topcheer/ggcode/internal/knight"
)

const knightPanelLeftWidth = 24

type knightPanelSection struct {
	ID    string
	Label string
	Icon  string
}

var knightSections = []knightPanelSection{
	{"status", "Status", "📊"},
	{"budget", "Budget", "💰"},
	{"queue", "Queue", "📋"},
	{"skills", "Skills", "🎯"},
	{"staging", "Staging", "📦"},
	{"proposals", "Proposals", "📝"},
	{"memory", "Memory", "🧠"},
	{"policies", "Policies", "⚙️"},
}

type knightPanelState struct {
	focus         int // 0=left nav, 1=right detail
	selectedIndex int
	detailIndex   int // selected item in right column
	scrollOffset  int
	detailScroll  int
	message       string
	messageTime   time.Time
}

func newKnightPanel() *knightPanelState {
	return &knightPanelState{}
}

// ---- open / close ----

func (m *Model) openKnightPanel() {
	if m.knight == nil {
		m.chatWriteSystem(nextSystemID(), m.t("knight.unavailable"))
		return
	}
	// Close other panels first
	m.streamPanel = nil
	m.modelPanel = nil
	m.mcpPanel = nil
	m.skillsPanel = nil
	m.inspectorPanel = nil
	m.knightPanel = newKnightPanel()
}

func (m *Model) closeKnightPanel() {
	m.knightPanel = nil
}

// ---- update ----

func (m *Model) updateKnightPanel(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	kp := m.knightPanel

	switch msg.String() {
	case "q", "esc":
		if kp.focus == 1 {
			kp.focus = 0
			return m, nil
		}
		m.closeKnightPanel()
		return m, nil
	case "tab":
		if kp.focus == 0 {
			kp.focus = 1
			kp.detailIndex = 0
			kp.detailScroll = 0
		} else {
			kp.focus = 0
		}
		return m, nil
	}

	if kp.focus == 0 {
		return m.updateKnightPanelLeft(msg)
	}
	return m.updateKnightPanelRight(msg)
}

func (m *Model) updateKnightPanelLeft(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	kp := m.knightPanel
	total := len(knightSections)

	switch msg.String() {
	case "up", "k":
		if kp.selectedIndex > 0 {
			kp.selectedIndex--
		}
	case "down", "j":
		if kp.selectedIndex < total-1 {
			kp.selectedIndex++
		}
	case "enter":
		kp.focus = 1
		kp.detailIndex = 0
		kp.detailScroll = 0
	}
	return m, nil
}

func (m *Model) updateKnightPanelRight(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	kp := m.knightPanel
	section := knightSections[kp.selectedIndex].ID
	maxItems := m.knightPanelItemCount(section)

	switch msg.String() {
	case "up", "k":
		if kp.detailIndex > 0 {
			kp.detailIndex--
		}
	case "down", "j":
		if kp.detailIndex < maxItems-1 {
			kp.detailIndex++
		}
	case "enter":
		return m.knightPanelAction(section, kp.detailIndex, "default")
	case "a":
		return m.knightPanelAction(section, kp.detailIndex, "approve")
	case "r":
		return m.knightPanelAction(section, kp.detailIndex, "reject")
	case "f":
		return m.knightPanelAction(section, kp.detailIndex, "freeze")
	case "u":
		return m.knightPanelAction(section, kp.detailIndex, "unfreeze")
	case "d":
		return m.knightPanelAction(section, kp.detailIndex, "delete")
	}
	return m, nil
}

func (m *Model) knightPanelItemCount(section string) int {
	if m.knight == nil {
		return 0
	}
	switch section {
	case "queue":
		items, _ := m.knight.Queue().List()
		return len(items)
	case "skills":
		active, _ := m.knight.Index().ActiveSkills()
		return len(active)
	case "staging":
		staging, _ := m.knight.Index().StagingSkills()
		return len(staging)
	case "proposals":
		proposals, _ := m.knight.RecentProjectImprovementProposals(50)
		return len(proposals)
	case "memory":
		entries, _ := m.knight.RecentSemanticMemory(50)
		return len(entries)
	case "policies":
		return len(m.knight.AutoPolicies())
	default:
		return 0
	}
}

func (m *Model) knightPanelAction(section string, idx int, action string) (tea.Model, tea.Cmd) {
	kp := m.knightPanel

	switch section {
	case "staging":
		staging, _ := m.knight.Index().StagingSkills()
		if idx < 0 || idx >= len(staging) {
			return m, nil
		}
		s := staging[idx]
		ref := knight.FormatSkillRefForDisplay(s.Scope, s.Name)
		switch action {
		case "approve":
			if err := m.knight.PromoteStagingByPath(s.Path); err != nil {
				kp.message = fmt.Sprintf("Error: %v", err)
			} else {
				kp.message = fmt.Sprintf("✅ Approved staging skill: %s", ref)
			}
		case "reject":
			if err := m.knight.RejectStagingByPath(s.Path); err != nil {
				kp.message = fmt.Sprintf("Error: %v", err)
			} else {
				kp.message = fmt.Sprintf("❌ Rejected staging skill: %s", ref)
			}
		}
		kp.messageTime = time.Now()

	case "proposals":
		proposals, _ := m.knight.RecentProjectImprovementProposals(50)
		if idx < 0 || idx >= len(proposals) {
			return m, nil
		}
		id := proposals[idx].ID
		switch action {
		case "approve":
			if _, err := m.knight.ApproveProposal(id, "approved via knight panel"); err != nil {
				kp.message = fmt.Sprintf("Error: %v", err)
			} else {
				kp.message = fmt.Sprintf("✅ Approved proposal: %s", id[:8])
			}
		case "reject":
			if _, err := m.knight.RejectProposal(id, "rejected via knight panel"); err != nil {
				kp.message = fmt.Sprintf("Error: %v", err)
			} else {
				kp.message = fmt.Sprintf("❌ Rejected proposal: %s", id[:8])
			}
		}
		kp.messageTime = time.Now()

	case "skills":
		active, _ := m.knight.Index().ActiveSkills()
		if idx < 0 || idx >= len(active) {
			return m, nil
		}
		s := active[idx]
		ref := knight.FormatSkillRefForDisplay(s.Scope, s.Name)
		switch action {
		case "freeze":
			if err := m.knight.SetSkillFrozen(ref, true); err != nil {
				kp.message = fmt.Sprintf("Error: %v", err)
			} else {
				kp.message = fmt.Sprintf("🧊 Frozen skill: %s", ref)
			}
		case "unfreeze":
			if err := m.knight.SetSkillFrozen(ref, false); err != nil {
				kp.message = fmt.Sprintf("Error: %v", err)
			} else {
				kp.message = fmt.Sprintf("🔓 Unfroze skill: %s", ref)
			}
		case "delete":
			if err := m.knight.DeleteSkill(ref); err != nil {
				kp.message = fmt.Sprintf("Error: %v", err)
			} else {
				kp.message = fmt.Sprintf("✕ Deleted skill: %s", ref)
				if kp.detailIndex >= len(active)-1 && kp.detailIndex > 0 {
					kp.detailIndex = len(active) - 2
				}
			}
		}
		kp.messageTime = time.Now()
	}

	return m, nil
}

// ---- render ----

func (m *Model) renderKnightPanel() string {
	kp := m.knightPanel
	if kp == nil {
		return ""
	}

	left := m.renderKnightPanelLeft()
	right := m.renderKnightPanelRight()
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func (m *Model) renderKnightPanelLeft() string {
	kp := m.knightPanel
	w := knightPanelLeftWidth

	var sb strings.Builder
	title := lipgloss.NewStyle().Bold(true).Padding(0, 1).Render("🌙 Knight")
	sb.WriteString(title)
	sb.WriteString("\n\n")

	for i, sec := range knightSections {
		label := fmt.Sprintf(" %s %s", sec.Icon, sec.Label)
		count := m.knightPanelItemCount(sec.ID)
		if count > 0 {
			label = fmt.Sprintf(" %s %s (%d)", sec.Icon, sec.Label, count)
		}
		style := lipgloss.NewStyle().Padding(0, 1)
		if i == kp.selectedIndex {
			style = style.Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("63"))
			if kp.focus == 0 {
				style = style.Foreground(lipgloss.Color("230")).Background(lipgloss.Color("99"))
			}
		}
		sb.WriteString(style.Width(w).Render(label))
		sb.WriteString("\n")
	}

	// Message
	if kp.message != "" && time.Since(kp.messageTime) < 3*time.Second {
		sb.WriteString("\n")
		msgStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Padding(0, 1).Width(w)
		sb.WriteString(msgStyle.Render(kp.message))
	}

	borderColor := lipgloss.Color("99")
	if kp.focus == 0 {
		borderColor = lipgloss.Color("183")
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(0, 1).
		Width(w).
		Height(20).
		Render(sb.String())
}

func (m *Model) renderKnightPanelRight() string {
	kp := m.knightPanel
	if kp.selectedIndex >= len(knightSections) {
		return ""
	}
	section := knightSections[kp.selectedIndex].ID
	w := m.viewWidth() - knightPanelLeftWidth - 6 // borders + padding
	if w < 30 {
		w = 30
	}

	var content string
	switch section {
	case "status":
		content = m.renderKnightStatus()
	case "budget":
		content = m.renderKnightBudget()
	case "queue":
		content = m.renderKnightQueue(w)
	case "skills":
		content = m.renderKnightSkills(w)
	case "staging":
		content = m.renderKnightStaging(w)
	case "proposals":
		content = m.renderKnightProposals(w)
	case "memory":
		content = m.renderKnightMemory(w)
	case "policies":
		content = m.renderKnightPolicies(w)
	default:
		content = "Unknown section"
	}

	borderColor := lipgloss.Color("99")
	if kp.focus == 1 {
		borderColor = lipgloss.Color("183")
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(0, 1).
		Width(w).
		Height(20).
		Render(content)
}

// ---- Section renderers ----

func (m *Model) renderKnightStatus() string {
	if m.knight == nil {
		return "Knight not available"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Status: %s\n\n", m.knight.Status()))

	used, remaining, limit := m.knight.BudgetStatus()
	if limit == 0 {
		sb.WriteString(fmt.Sprintf("Budget: %d tokens used / unlimited\n", used))
	} else {
		pct := float64(used) / float64(limit) * 100
		barW := 20
		filled := int(pct / 100 * float64(barW))
		bar := strings.Repeat("█", filled) + strings.Repeat("░", barW-filled)
		sb.WriteString(fmt.Sprintf("Budget: [%s] %.0f%%\n", bar, pct))
		sb.WriteString(fmt.Sprintf("  %d used / %d remaining / %d total\n", used, remaining, limit))
	}

	sb.WriteString("\n")
	if evals, err := m.knight.RecentAutoPromoteEvals(3); err == nil && len(evals) > 0 {
		sb.WriteString("Recent auto-promote evals:\n")
		for _, eval := range evals {
			sb.WriteString(fmt.Sprintf("  • %s\n", formatAutoPromoteEval(eval)))
		}
	}
	if scenarios, err := m.knight.RecentSkillScenarios(3); err == nil && len(scenarios) > 0 {
		sb.WriteString("\nRecent replay scenarios:\n")
		for _, scenario := range scenarios {
			sb.WriteString(fmt.Sprintf("  • %s\n", formatSkillScenario(scenario)))
		}
	}
	return sb.String()
}

func (m *Model) renderKnightBudget() string {
	if m.knight == nil {
		return "Knight not available"
	}
	var sb strings.Builder
	used, remaining, limit := m.knight.BudgetStatus()
	if limit == 0 {
		sb.WriteString(fmt.Sprintf("Token Budget: %d used / unlimited\n\n", used))
	} else {
		sb.WriteString(fmt.Sprintf("Token Budget: %d used / %d remaining / %d total\n\n", used, remaining, limit))
	}
	sb.WriteString("Bucket budgets reset daily.\n")
	return sb.String()
}

func (m *Model) renderKnightQueue(w int) string {
	if m.knight == nil {
		return "Knight not available"
	}
	items, err := m.knight.Queue().List()
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	if len(items) == 0 {
		return "No deferred candidates in queue"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Candidate Queue (%d):\n\n", len(items)))
	for i, item := range items {
		prefix := "  "
		if m.knightPanel.detailIndex == i {
			prefix = "▶ "
		}
		age := "new"
		if !item.FirstQueuedAt.IsZero() {
			age = fmt.Sprintf("%dd", int(time.Since(item.FirstQueuedAt).Hours()/24))
		}
		line := fmt.Sprintf("%s%s:%s [pri=%.1f touches=%d age=%s cat=%s]",
			prefix, item.Scope, item.Name, item.QueuePriority, item.QueueTouchCount, age, item.Category)
		sb.WriteString(line + "\n")
		if m.knightPanel.detailIndex == i {
			sb.WriteString(fmt.Sprintf("    Evidence: %d | %s\n", item.EvidenceCount,
				util.Truncate(item.QueuePriorityReason, w-6)))
		}
	}
	return sb.String()
}

func (m *Model) renderKnightSkills(w int) string {
	if m.knight == nil {
		return "Knight not available"
	}
	active, _ := m.knight.Index().ActiveSkills()
	if len(active) == 0 {
		return "No active skills"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Active Skills (%d):\n\n", len(active)))
	for i, s := range active {
		prefix := "  "
		if m.knightPanel.detailIndex == i {
			prefix = "▶ "
		}
		ref := knight.FormatSkillRefForDisplay(s.Scope, s.Name)
		used, _, _ := m.knight.SkillUsage(ref)
		avg, samples := m.knight.SkillFeedback(ref)
		frozen := ""
		if s.Meta.Frozen {
			frozen = " 🧊"
		}
		sb.WriteString(fmt.Sprintf("%s%s (used=%d avg=%.1f n=%d scope=%s)%s\n",
			prefix, ref, used, avg, samples, s.Scope, frozen))
		if m.knightPanel.detailIndex == i {
			sb.WriteString(fmt.Sprintf("    %s\n", s.Meta.Description))
			sb.WriteString("    [f] freeze  [u] unfreeze  [d] delete\n")
		}
	}
	return sb.String()
}

func (m *Model) renderKnightStaging(w int) string {
	if m.knight == nil {
		return "Knight not available"
	}
	staging, _ := m.knight.Index().StagingSkills()
	if len(staging) == 0 {
		return "No staging skills awaiting review"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Staging Skills (%d):\n\n", len(staging)))
	for i, s := range staging {
		prefix := "  "
		if m.knightPanel.detailIndex == i {
			prefix = "▶ "
		}
		result := knight.ValidateSkill(s)
		validIcon := "✅"
		if !result.Valid {
			validIcon = "❌"
		}
		sb.WriteString(fmt.Sprintf("%s%s (%s) %s\n", prefix, s.Name, s.Scope, validIcon))
		if m.knightPanel.detailIndex == i {
			sb.WriteString(fmt.Sprintf("    %s\n", s.Meta.Description))
			if len(result.Warnings) > 0 {
				for _, w := range result.Warnings {
					sb.WriteString(fmt.Sprintf("    ⚠️  %s\n", w))
				}
			}
			sb.WriteString("    [a] approve  [r] reject\n")
		}
	}
	return sb.String()
}

func (m *Model) renderKnightProposals(w int) string {
	if m.knight == nil {
		return "Knight not available"
	}
	proposals, err := m.knight.RecentProjectImprovementProposals(20)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	if len(proposals) == 0 {
		return "No project improvement proposals"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Project Proposals (%d):\n\n", len(proposals)))
	for i, p := range proposals {
		prefix := "  "
		if m.knightPanel.detailIndex == i {
			prefix = "▶ "
		}
		shortID := p.ID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		sb.WriteString(fmt.Sprintf("%s%s %s [%s]\n", prefix, shortID, p.Goal, p.Status))
		if m.knightPanel.detailIndex == i {
			sb.WriteString(fmt.Sprintf("    Path: %s\n", p.Path))
			sb.WriteString(fmt.Sprintf("    Created: %s\n", p.Time.Format("2006-01-02")))
			if p.Status == "pending" {
				sb.WriteString("    [a] approve  [r] reject\n")
			}
		}
	}
	return sb.String()
}

func (m *Model) renderKnightMemory(w int) string {
	if m.knight == nil {
		return "Knight not available"
	}
	entries, err := m.knight.RecentSemanticMemory(20)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	if len(entries) == 0 {
		return "No semantic memory entries"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Semantic Memory (%d):\n\n", len(entries)))
	for i, e := range entries {
		prefix := "  "
		if m.knightPanel.detailIndex == i {
			prefix = "▶ "
		}
		sb.WriteString(fmt.Sprintf("%s[%s] %s\n", prefix, e.Kind, util.Truncate(e.Summary, w-10)))
		if m.knightPanel.detailIndex == i {
			sb.WriteString(fmt.Sprintf("    Source: %s | Session: %s\n", e.Source, util.Truncate(e.SessionID, 12)))
			if len(e.Refs) > 0 {
				sb.WriteString(fmt.Sprintf("    Refs: %s\n", strings.Join(e.Refs, ", ")))
			}
		}
	}
	return sb.String()
}

func (m *Model) renderKnightPolicies(w int) string {
	if m.knight == nil {
		return "Knight not available"
	}
	policies := m.knight.AutoPolicies()
	if len(policies) == 0 {
		return "No auto policies configured"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Auto Policies (%d):\n\n", len(policies)))
	for i, p := range policies {
		prefix := "  "
		if m.knightPanel.detailIndex == i {
			prefix = "▶ "
		}
		status := "active"
		if !p.Effective {
			status = "inactive"
		}
		sb.WriteString(fmt.Sprintf("%s%s [%s] trust=%s\n", prefix, p.Name, status, p.Mode))
		if m.knightPanel.detailIndex == i {
			sb.WriteString(fmt.Sprintf("    %s\n", p.Description))
			if p.Reason != "" {
				sb.WriteString(fmt.Sprintf("    Reason: %s\n", p.Reason))
			}
		}
	}
	return m.renderContextBox("/knight", sb.String(), lipgloss.Color("13"))
}
