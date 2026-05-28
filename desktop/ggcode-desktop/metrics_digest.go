package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/metrics"
)

func (b *AgentBridge) appendTurnMetricsDigest(turnIndex int) {
	b.mu.Lock()
	if turnIndex <= 0 || turnIndex <= b.lastMetricDigestTurn || b.currentSes == nil || b.ui == nil {
		b.mu.Unlock()
		return
	}
	ses := b.currentSes
	ui := b.ui
	b.mu.Unlock()

	turn, ok := metrics.TurnSummaryForIndex(ses.MetricsForEndpoint(ses.Vendor, ses.Endpoint), turnIndex)
	if !ok {
		return
	}

	ui.AppendChat(ChatMessage{
		Role:         "system",
		Content:      formatDesktopTurnMetricsDigest(turn),
		Time:         time.Now(),
		PreventMerge: true,
	})

	b.mu.Lock()
	if turnIndex > b.lastMetricDigestTurn {
		b.lastMetricDigestTurn = turnIndex
	}
	b.mu.Unlock()
}

func formatDesktopTurnMetricsDigest(turn metrics.TurnSummary) string {
	turnLabel := fmt.Sprintf("📊 %s #%d", t("metrics.turn_digest_turn"), turn.TurnIndex)
	if strings.HasPrefix(t("metrics.turn_digest_turn"), "第") {
		turnLabel = fmt.Sprintf("📊 %s %d 轮", t("metrics.turn_digest_turn"), turn.TurnIndex)
	}
	parts := []string{
		turnLabel,
		fmt.Sprintf("%s %s", t("sidebar.metric_avg_ttft"), humanizeMetricDuration(turn.TTFT)),
		fmt.Sprintf("%s %s", t("metrics.turn_digest_duration"), humanizeMetricDuration(turn.Duration)),
		fmt.Sprintf("%s %s", t("sidebar.metric_avg_think"), humanizeMetricDuration(turn.ThinkTime)),
		fmt.Sprintf("%s %d", t("metrics.turn_digest_tools"), turn.ToolCallCount),
	}
	if turn.SlowestTool != "" {
		parts = append(parts, fmt.Sprintf("%s %s %s", t("metrics.turn_digest_slowest"), turn.SlowestTool, humanizeMetricDuration(turn.SlowestToolDuration)))
	}
	if turn.ToolFailureCount > 0 {
		parts = append(parts, t("metrics.turn_digest_failed"))
	}
	return strings.Join(parts, " · ")
}
