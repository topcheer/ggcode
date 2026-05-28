package tui

import (
	"fmt"
	"strings"

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
	m.chatWriteLocalSystem(nextSystemID(), formatTurnMetricsDigest(m.currentLanguage(), turn))
	m.lastMetricDigestTurn = turnIndex
}

func formatTurnMetricsDigest(lang Language, turn metrics.TurnSummary) string {
	parts := []string{
		turnMetricsDigestText(lang, "turn", turn.TurnIndex),
		fmt.Sprintf("%s %s", turnMetricsDigestText(lang, "ttft"), formatMetricDuration(turn.TTFT)),
		fmt.Sprintf("%s %s", turnMetricsDigestText(lang, "duration"), formatMetricDuration(turn.Duration)),
		fmt.Sprintf("%s %s", turnMetricsDigestText(lang, "think"), formatMetricDuration(turn.ThinkTime)),
		fmt.Sprintf("%s %d", turnMetricsDigestText(lang, "tools"), turn.ToolCallCount),
	}
	if turn.SlowestTool != "" {
		parts = append(parts, fmt.Sprintf("%s %s %s", turnMetricsDigestText(lang, "slowest"), turn.SlowestTool, formatMetricDuration(turn.SlowestToolDuration)))
	}
	if turn.ToolFailureCount > 0 {
		parts = append(parts, turnMetricsDigestText(lang, "failed"))
	}
	return strings.Join(parts, " · ")
}

func turnMetricsDigestText(lang Language, key string, args ...interface{}) string {
	switch lang {
	case LangZhCN:
		switch key {
		case "turn":
			return fmt.Sprintf("📊 第 %d 轮", args[0].(int))
		case "ttft":
			return "首字"
		case "duration":
			return "时长"
		case "think":
			return "思考"
		case "tools":
			return "工具"
		case "slowest":
			return "最慢"
		case "failed":
			return "!"
		}
	default:
		switch key {
		case "turn":
			return fmt.Sprintf("📊 Turn #%d", args[0].(int))
		case "ttft":
			return "TTFT"
		case "duration":
			return "Dur"
		case "think":
			return "Think"
		case "tools":
			return "Tools"
		case "slowest":
			return "Slowest"
		case "failed":
			return "!"
		}
	}
	return key
}
