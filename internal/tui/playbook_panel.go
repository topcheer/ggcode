package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/agent"
)

// playbookPanel is the observability surface for ggcode's harness
// self-evolution layer (r76-r83): task strategies (playbook.json) and
// learned rules (agent-rules.json). /rules already lists rule text;
// this panel adds the health lens the layer lacked: playbook entries
// were entirely invisible, and rules had no staleness/category view.
// Read-only: both stores are re-opened from disk on open, so the panel
// never races the agent's writers.

type playbookPanelState struct {
	viewport ViewportModel
}

func (m *Model) openPlaybookPanel() {
	panel := &playbookPanelState{viewport: newViewport()}
	m.playbookPanel = panel
	m.syncPlaybookPanelViewport(true)
}

func (m *Model) closePlaybookPanel() {
	m.playbookPanel = nil
}

func (m Model) renderPlaybookPanel() string {
	if m.playbookPanel == nil {
		return ""
	}
	width := m.panelContentWidth()
	contentWidth := max(12, width)
	meta := playbookPanelText(m.currentLanguage(), "meta")
	if scroll := m.playbookPanel.viewport.ScrollIndicatorStyle(); scroll != "" {
		meta += "  •  " + scroll
	}
	content := strings.Join([]string{
		lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true).Render(playbookPanelText(m.currentLanguage(), "title")),
		truncateDisplayWidth(meta, contentWidth),
		m.playbookPanel.viewport.View().Content,
		lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(playbookPanelText(m.currentLanguage(), "hint")),
	}, "\n")
	return m.renderContextBox(playbookPanelText(m.currentLanguage(), "title"), content, lipgloss.Color("10"))
}

func (m *Model) syncPlaybookPanelViewport(initial bool) {
	if m.playbookPanel == nil {
		return
	}
	width := m.panelContentWidth()
	height := m.panelContentHeight()
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	oldOffset := m.playbookPanel.viewport.YOffset()
	m.playbookPanel.viewport.autoFollow = false
	m.playbookPanel.viewport.SetSize(width, height)
	m.playbookPanel.viewport.SetContent(m.playbookPanelBody())
	maxOffset := max(0, m.playbookPanel.viewport.TotalLineCount()-m.playbookPanel.viewport.VisibleLineCount())
	if initial {
		m.playbookPanel.viewport.vp.SetYOffset(0)
		return
	}
	m.playbookPanel.viewport.vp.SetYOffset(min(maxOffset, oldOffset))
}

// playbookPanelBody loads fresh snapshots of both self-evolution stores from
// disk and renders the digest. Errors degrade to an empty view (missing
// stores are a normal first-run state, not a failure).
func (m Model) playbookPanelBody() string {
	workingDir := ""
	if m.agent != nil {
		workingDir = m.agent.WorkingDir()
	}
	if workingDir == "" {
		return playbookPanelText(m.currentLanguage(), "empty")
	}
	return playbookBodyForDir(workingDir)
}

// playbookBodyForDir is the dir-parameterized core of the panel body,
// kept separate so tests can exercise the read path without a full agent.
func playbookBodyForDir(workingDir string) string {
	now := time.Now()
	var entries []agent.PlaybookEntry
	if pb := agent.NewPlaybook(workingDir); pb != nil {
		entries = pb.Snapshot()
	}
	var rules []agent.Rule
	if rs := agent.NewRuleStore(workingDir); rs != nil {
		rules = rs.Rules()
	}
	if len(entries) == 0 && len(rules) == 0 {
		return playbookPanelText(LangEnglish, "empty")
	}
	return agent.FormatPlaybookDigest(entries, rules, now)
}

func playbookPanelText(lang Language, key string) string {
	switch lang {
	case LangZhCN:
		switch key {
		case "title":
			return "自进化健康"
		case "meta":
			return fmt.Sprintf("playbook 与学习规则（%s）", time.Now().Format("2006-01-02"))
		case "hint":
			return "j/k 或 PgUp/PgDn 滚动 • Esc 关闭"
		case "empty":
			return "还没有 playbook 数据或学习规则。它们会在成功运行与失败复盘后逐步积累。"
		}
	default:
		switch key {
		case "title":
			return "Harness self-evolution"
		case "meta":
			return fmt.Sprintf("playbook & learned rules (%s)", time.Now().Format("2006-01-02"))
		case "hint":
			return "j/k or PgUp/PgDn scroll • Esc closes"
		case "empty":
			return "No playbook data or learned rules yet. They accumulate after successful runs and failure post-mortems."
		}
	}
	return key
}
