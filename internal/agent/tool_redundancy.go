package agent

import (
	"fmt"
	"strings"
)

const (
	scatterDupWarnThreshold = 3
	scatterDupMaxWarnings   = 2
)

type toolRedundancyState struct {
	counts   map[string]int
	tools    map[string]string
	warnings int
}

func newToolRedundancyAnalyzer() *toolRedundancyState {
	return &toolRedundancyState{
		counts: make(map[string]int),
		tools:  make(map[string]string),
	}
}

func (t *toolRedundancyState) reset() {
	t.counts = make(map[string]int)
	t.tools = make(map[string]string)
	t.warnings = 0
}

func (t *toolRedundancyState) recordCall(toolName string, args []byte) string {
	if t.warnings >= scatterDupMaxWarnings {

		fp := fingerprintToolCall(toolName, args)
		t.counts[fp]++
		return ""
	}

	fp := fingerprintToolCall(toolName, args)
	t.counts[fp]++
	t.tools[fp] = toolName

	count := t.counts[fp]
	if count == scatterDupWarnThreshold {
		t.warnings++

		return fmt.Sprintf(
			"Efficiency hint: You have called %s with identical arguments %d times in this session. "+
				"If the earlier results are still valid for your current question, reuse them "+
				"instead of re-invoking the same call.",
			toolName, count,
		)
	}

	if count == scatterDupWarnThreshold*2 {
		t.warnings++
		return fmt.Sprintf(
			"Warning: %s called %d times with identical arguments. This is consuming significant iteration budget. "+
				"Consider a different approach: narrow your search, batch your operations, or use the information you already have.",
			toolName, count,
		)
	}

	return ""
}

func (t *toolRedundancyState) summary() string {
	if len(t.counts) == 0 {
		return ""
	}
	var parts []string
	for fp, count := range t.counts {
		if count >= 2 {
			name := t.tools[fp]
			parts = append(parts, fmt.Sprintf("%s x%d", name, count))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ", ")
}
