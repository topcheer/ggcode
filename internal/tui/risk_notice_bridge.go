package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/agent"
)

// risk_notice_bridge.go -- renders agent risk notices as system chat
// items. Safety Nudges (arXiv:2609.26865): real-time user-facing risk
// awareness is a first-class agent-safety surface; until now the
// irreversibility gate and permission denials were model-facing only.
// Notices arrive via Agent.SetRiskNoticeHandler -> sendProgramMsgs.

type riskNoticeMsg struct {
	notice agent.RiskNotice
}

func (m Model) handleRiskNoticeMsg(msg riskNoticeMsg) (tea.Model, tea.Cmd) {
	text := formatRiskNotice(msg.notice)
	if text == "" {
		return m, nil
	}
	m.chatWriteSystem(nextSystemID(), text)
	m.chatListFollowOutput()
	return m, nil
}

// formatRiskNotice renders one compact user-facing line per risk event.
// Shape: "[risk] source - tool (mode): detail".
func formatRiskNotice(n agent.RiskNotice) string {
	if strings.TrimSpace(n.Source) == "" {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[risk] %s", n.Source)
	if n.Tool != "" {
		fmt.Fprintf(&b, " - %s", n.Tool)
	}
	if n.Mode != "" {
		fmt.Fprintf(&b, " (mode %s)", n.Mode)
	}
	if n.Detail != "" {
		fmt.Fprintf(&b, ": %s", n.Detail)
	}
	return b.String()
}
