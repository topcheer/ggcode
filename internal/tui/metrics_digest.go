package tui

import (
	"github.com/topcheer/ggcode/internal/metrics"
)

func (m *Model) appendTurnMetricsDigest(turnIndex int) {
	if turnIndex <= 0 || turnIndex <= m.lastMetricDigestTurn || m.session == nil {
		return
	}
	turn, ok := metrics.TurnSummaryForIndex(m.sidebarSessionMetrics(), turnIndex)
	if !ok {
		return
	}
	lang := "en"
	if m.currentLanguage() == LangZhCN {
		lang = "zh-CN"
	}
	m.chatWriteSystem(nextSystemID(), metrics.FormatTurnDigest(lang, turn))
	m.lastMetricDigestTurn = turnIndex
}
