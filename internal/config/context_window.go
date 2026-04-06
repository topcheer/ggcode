package config

import (
	"regexp"
	"strconv"
	"strings"
)

const defaultContextWindow = 128000
const defaultMaxOutputTokens = 8192

type modelCapability struct {
	ContextWindow   int
	MaxOutputTokens int
}

var knownModelCapabilities = map[string]modelCapability{
	"ark-code-latest": {ContextWindow: 200000},
	"kimi-for-coding": {ContextWindow: 262144, MaxOutputTokens: 32768},
	"minimax-m2.7":    {ContextWindow: 204800, MaxOutputTokens: 2048},
	"glm-5":           {ContextWindow: 200000, MaxOutputTokens: 128000},
	"glm-5-turbo":     {ContextWindow: 200000, MaxOutputTokens: 128000},
	"glm-5.1":         {ContextWindow: 200000, MaxOutputTokens: 128000},
	"glm-4.7":         {ContextWindow: 200000, MaxOutputTokens: 128000},
	"glm-4.7-flashx":  {ContextWindow: 200000, MaxOutputTokens: 128000},
	"glm-4.6":         {ContextWindow: 200000, MaxOutputTokens: 128000},
	"glm-4.5-air":     {ContextWindow: 128000, MaxOutputTokens: 96000},
}

var contextWindowHintPattern = regexp.MustCompile(`(^|[^0-9])(\d+)(k|m)($|[^a-z0-9])`)

// inferContextWindow resolves an approximate input context window.
// Explicit endpoint config should override this; this heuristic exists so
// auto-compaction can track common models more accurately than a fixed 128k.
func inferContextWindow(model, protocol string) int {
	if cap, ok := lookupModelCapability(model); ok && cap.ContextWindow > 0 {
		return cap.ContextWindow
	}
	if hinted := parseContextWindowHint(model); hinted > 0 {
		return hinted
	}

	m := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.Contains(m, "claude"):
		return 200000
	case strings.Contains(m, "gemini-1.5"),
		strings.Contains(m, "gemini-2.0"),
		strings.Contains(m, "gemini-2.5"):
		return 1000000
	case strings.Contains(m, "gpt-4o"),
		strings.Contains(m, "gpt-4.1"),
		strings.Contains(m, "gpt-5"),
		strings.Contains(m, "glm-"),
		strings.Contains(m, "deepseek"),
		strings.Contains(m, "mistral"),
		strings.Contains(m, "llama-3.1"),
		strings.Contains(m, "moonshot"),
		strings.Contains(m, "qwen"),
		strings.Contains(m, "kimi"):
		return defaultContextWindow
	}

	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "anthropic":
		return 200000
	case "gemini":
		return 1000000
	default:
		return defaultContextWindow
	}
}

func inferMaxOutputTokens(model, protocol string) int {
	if cap, ok := lookupModelCapability(model); ok && cap.MaxOutputTokens > 0 {
		return cap.MaxOutputTokens
	}

	switch strings.ToLower(strings.TrimSpace(protocol)) {
	default:
		return defaultMaxOutputTokens
	}
}

func lookupModelCapability(model string) (modelCapability, bool) {
	cap, ok := knownModelCapabilities[strings.ToLower(strings.TrimSpace(model))]
	return cap, ok
}

func parseContextWindowHint(model string) int {
	matches := contextWindowHintPattern.FindAllStringSubmatch(strings.ToLower(model), -1)
	best := 0
	for _, match := range matches {
		if len(match) < 4 {
			continue
		}
		n, err := strconv.Atoi(match[2])
		if err != nil || n <= 0 {
			continue
		}
		switch match[3] {
		case "k":
			n *= 1000
		case "m":
			n *= 1000000
		}
		if n > best {
			best = n
		}
	}
	return best
}
