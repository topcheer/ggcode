package metrics

import (
	"fmt"
	"strings"
	"time"
)

// FormatDuration formats a duration for human-readable display.
func FormatDuration(d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d/time.Millisecond)
	}
	seconds := d.Seconds()
	if seconds < 10 {
		return fmt.Sprintf("%.1fs", seconds)
	}
	if d < time.Minute {
		return fmt.Sprintf("%.0fs", seconds)
	}
	return d.Round(time.Second).String()
}

// FormatTurnDigest produces a single-line summary of a turn's metrics.
// lang is a BCP-47 language code (e.g. "en", "zh-CN").
func FormatTurnDigest(lang string, turn TurnSummary) string {
	parts := []string{
		digestText(lang, "turn", turn.TurnIndex),
		fmt.Sprintf("%s %s", digestText(lang, "ttft"), FormatDuration(turn.TTFT)),
		fmt.Sprintf("%s %s", digestText(lang, "duration"), FormatDuration(turn.Duration)),
		fmt.Sprintf("%s %s", digestText(lang, "think"), FormatDuration(turn.ThinkTime)),
		fmt.Sprintf("%s %d", digestText(lang, "tools"), turn.ToolCallCount),
	}
	if turn.SlowestTool != "" {
		parts = append(parts, fmt.Sprintf("%s %s %s", digestText(lang, "slowest"), turn.SlowestTool, FormatDuration(turn.SlowestToolDuration)))
	}
	if turn.ToolFailureCount > 0 {
		parts = append(parts, digestText(lang, "failed"))
	}
	return strings.Join(parts, " · ")
}

func digestText(lang, key string, args ...interface{}) string {
	m, ok := digestTranslations[lang]
	if !ok {
		m = digestTranslations["en"]
	}
	t, ok := m[key]
	if !ok {
		t = digestTranslations["en"][key]
	}
	if args != nil {
		return fmt.Sprintf(t, args...)
	}
	return t
}

var digestTranslations = map[string]map[string]string{
	"zh-CN": {
		"turn":     "\U0001F4CA 第 %d 轮",
		"ttft":     "首字",
		"duration": "时长",
		"think":    "思考",
		"tools":    "工具",
		"slowest":  "最慢",
		"failed":   "!",
	},
	"en": {
		"turn":     "\U0001F4CA Turn #%d",
		"ttft":     "TTFT",
		"duration": "Dur",
		"think":    "Think",
		"tools":    "Tools",
		"slowest":  "Slowest",
		"failed":   "!",
	},
}
