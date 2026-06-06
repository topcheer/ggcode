package uiusage

import "github.com/topcheer/ggcode/internal/provider"

type ContextDisplay struct {
	UsedTokens       int
	MaxTokens        int
	Threshold        int
	UsagePercent     int
	RemainingPercent int
	UsedLabel        string
	MaxLabel         string
}

type SessionUsageDisplay struct {
	TotalLabel      string
	InputLabel      string
	OutputLabel     string
	CacheReadLabel  string
	CacheWriteLabel string
	CacheHitPercent int
}

func BuildContextDisplay(usedTokens, maxTokens, threshold int) (ContextDisplay, bool) {
	if maxTokens <= 0 || threshold <= 0 {
		return ContextDisplay{}, false
	}

	usagePercent := int(float64(usedTokens) / float64(maxTokens) * 100)
	if usagePercent < 0 {
		usagePercent = 0
	}
	if usagePercent > 100 {
		usagePercent = 100
	}

	remainingPercent := int(float64(threshold-usedTokens) / float64(threshold) * 100)
	if remainingPercent < 0 {
		remainingPercent = 0
	}
	if remainingPercent > 100 {
		remainingPercent = 100
	}

	return ContextDisplay{
		UsedTokens:       usedTokens,
		MaxTokens:        maxTokens,
		Threshold:        threshold,
		UsagePercent:     usagePercent,
		RemainingPercent: remainingPercent,
		UsedLabel:        HumanizeTokenCount(usedTokens),
		MaxLabel:         HumanizeTokenCount(maxTokens),
	}, true
}

func BuildSessionUsageDisplay(usage provider.TokenUsage) SessionUsageDisplay {
	return SessionUsageDisplay{
		TotalLabel:      HumanizeTokenCount(usage.Total()),
		InputLabel:      HumanizeTokenCount(usage.DisplayInputTokens()),
		OutputLabel:     HumanizeTokenCount(usage.OutputTokens),
		CacheReadLabel:  HumanizeTokenCount(usage.CacheRead),
		CacheWriteLabel: HumanizeTokenCount(usage.CacheWrite),
		CacheHitPercent: usage.CacheHitPercent(),
	}
}

func HumanizeTokenCount(n int) string {
	switch {
	case n >= 1_000_000 && n%1_000_000 == 0:
		return itoa(n/1_000_000) + "m"
	case n >= 1000 && n%1000 == 0:
		return itoa(n/1000) + "k"
	default:
		return itoa(n)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [32]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + (n % 10))
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
