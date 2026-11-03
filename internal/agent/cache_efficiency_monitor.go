package agent

import (
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

const (
	cacheEffWindow = 8

	cacheEffMinCalls = 4

	cacheHitRatioThreshold = 0.50

	cacheBustRatioThreshold = 0.15

	cacheStormConsecutive = 3
)

type cacheEffSample struct {
	input     int
	cacheRead int
	total     int
}

type cacheEffMonitor struct {
	mu sync.Mutex

	samples    []cacheEffSample
	warmSeen   bool
	coldStreak int
	alerted    bool
}

func newCacheEffMonitor() *cacheEffMonitor {
	return &cacheEffMonitor{
		samples: make([]cacheEffSample, 0, cacheEffWindow+1),
	}
}

func (m *cacheEffMonitor) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.samples = m.samples[:0]
	m.warmSeen = false
	m.coldStreak = 0
	m.alerted = false
}

func (m *cacheEffMonitor) record(usage provider.TokenUsage) string {

	m.mu.Lock()
	defer m.mu.Unlock()

	sample := cacheEffSample{
		input:     usage.InputTokens,
		cacheRead: usage.CacheRead,
		total:     usage.DisplayInputTokens() + usage.CacheRead,
	}

	m.samples = append(m.samples, sample)
	if len(m.samples) > cacheEffWindow {
		m.samples = m.samples[1:]
	}

	if m.alerted {
		return ""
	}

	if len(m.samples) < cacheEffMinCalls {
		return ""
	}

	hasCacheActivity := false
	for _, samp := range m.samples {
		if samp.cacheRead > 0 {
			hasCacheActivity = true
			break
		}
	}
	if !hasCacheActivity {
		return ""
	}

	ratio := m.hitRatio(sample)

	if ratio >= cacheHitRatioThreshold {
		m.warmSeen = true
		m.coldStreak = 0
	} else if ratio <= cacheBustRatioThreshold {
		if m.warmSeen {
			m.coldStreak++
		}
	} else {

	}

	if m.coldStreak >= cacheStormConsecutive {
		m.alerted = true
		guidance := m.formatStormGuidance()
		debug.Log("cache-efficiency", "cache bust storm detected: coldStreak=%d window=%s", m.coldStreak, m.windowSummary())
		return guidance
	}

	return ""
}

func (m *cacheEffMonitor) hitRatio(s cacheEffSample) float64 {
	if s.total == 0 {
		return 0
	}
	return float64(s.cacheRead) / float64(s.total)
}

func (m *cacheEffMonitor) formatStormGuidance() string {
	var sb strings.Builder

	sb.WriteString("[Cache Efficiency Alert] Prompt cache is being repeatedly invalidated. ")
	sb.WriteString(fmt.Sprintf("Cache hit ratio dropped from warm to ~0%% over %d consecutive calls. ", m.coldStreak))
	sb.WriteString("This means the API is re-processing the full system prompt + conversation prefix each turn, ")
	sb.WriteString("costing significantly more tokens (1.25x for cache writes vs 0.1x for reads).\n\n")
	sb.WriteString("Likely causes and fixes:\n")
	sb.WriteString("  - System prompt instability: intelligence gates or dynamic injections are modifying the system prompt each turn. ")
	sb.WriteString("Move advisory messages to user/tool messages instead of system prompt.\n")
	sb.WriteString("  - Tool list churn: adding/removing tools mid-run busts cache from the tools breakpoint. ")
	sb.WriteString("Stabilize the tool set early in the run.\n")
	sb.WriteString("  - Pinned context updates: injecting new pinned items modifies the system prompt prefix. ")
	sb.WriteString("Batch pin operations rather than adding one item per turn.\n")
	sb.WriteString("  - Reasoning effort changes: frequent effort level switches may affect cache stability on some providers.\n\n")
	sb.WriteString(fmt.Sprintf("Recent window: %s", m.windowSummary()))

	return sb.String()
}

func (m *cacheEffMonitor) windowSummary() string {
	if len(m.samples) == 0 {
		return "(empty)"
	}
	var sb strings.Builder
	for idx, samp := range m.samples {
		if idx > 0 {
			sb.WriteString(" -> ")
		}
		ratio := 0.0
		if samp.total > 0 {
			ratio = float64(samp.cacheRead) / float64(samp.total) * 100
		}
		fmt.Fprintf(&sb, "[%d: in=%d cr=%d %.0f%%]", idx, samp.input, samp.cacheRead, ratio)
	}
	return sb.String()
}
