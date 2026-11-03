package agent

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

var latencyMonitoredTools = map[string]bool{
	"read_file":               true,
	"multi_file_read":         true,
	"search_files":            true,
	"grep":                    true,
	"glob":                    true,
	"list_directory":          true,
	"edit_file":               true,
	"multi_edit_file":         true,
	"multi_file_edit":         true,
	"lsp_symbols":             true,
	"lsp_definition":          true,
	"lsp_references":          true,
	"lsp_hover":               true,
	"lsp_workspace_symbols":   true,
	"lsp_document_highlights": true,
	"lsp_implementation":      true,
	"lsp_diagnostics":         true,
	"code_search":             true,
}

const latencyMinSamples = 3

const outlierRunThreshold = 3

const latencyOutlierMultiplier = 5.0

const latencyAbsoluteFloor = 2 * time.Second

const latencyWarnCooldown = 5 * time.Minute

type latencySample struct {
	dur      time.Duration
	recorded time.Time
}

type LatencyTracker struct {
	mu         sync.Mutex
	samples    map[string][]latencySample
	lastWarn   map[string]time.Time
	outlierRun map[string]int
}

func NewLatencyTracker() *LatencyTracker {
	return &LatencyTracker{
		samples:    make(map[string][]latencySample),
		lastWarn:   make(map[string]time.Time),
		outlierRun: make(map[string]int),
	}
}

const maxLatencySamples = 20

func (lt *LatencyTracker) RecordAndCheck(toolName string, dur time.Duration) string {
	if lt == nil {
		return ""
	}

	lt.mu.Lock()
	defer lt.mu.Unlock()

	samples := lt.samples[toolName]

	var warning string
	isOutlier := false
	if latencyMonitoredTools[toolName] {
		warning, isOutlier = lt.checkOutlier(toolName, dur, samples)
	}

	if !isOutlier || lt.outlierRun[toolName] >= outlierRunThreshold {
		samples = append(samples, latencySample{dur: dur, recorded: time.Now()})
		if len(samples) > maxLatencySamples {
			samples = samples[len(samples)-maxLatencySamples:]
		}
	}

	if isOutlier {
		lt.outlierRun[toolName]++
	} else {
		lt.outlierRun[toolName] = 0
	}
	lt.samples[toolName] = samples

	return warning
}

func (lt *LatencyTracker) checkOutlier(toolName string, dur time.Duration, samples []latencySample) (string, bool) {

	if len(samples) < latencyMinSamples {
		return "", false
	}

	if dur < latencyAbsoluteFloor {
		return "", false
	}

	var total time.Duration
	for _, s := range samples {
		total += s.dur
	}
	mean := total / time.Duration(len(samples))
	if mean <= 0 {
		return "", false
	}

	if float64(dur) < float64(mean)*latencyOutlierMultiplier {
		return "", false
	}

	if last, ok := lt.lastWarn[toolName]; ok {
		if time.Since(last) < latencyWarnCooldown {
			return "", true
		}
	}
	lt.lastWarn[toolName] = time.Now()

	return formatLatencyWarning(toolName, dur, mean), true
}

func formatLatencyWarning(toolName string, dur, mean time.Duration) string {
	ratio := float64(dur) / float64(mean)
	if ratio < 1 {
		ratio = 1
	}

	var hint string
	switch {

	case strings.Contains(toolName, "edit"):
		hint = "this should normally be fast — check if the file is unusually large."
	case strings.Contains(toolName, "read") || strings.Contains(toolName, "file"):
		hint = "consider using offset/limit to read only the relevant section."
	case strings.Contains(toolName, "search") || strings.Contains(toolName, "grep") || toolName == "glob":
		hint = "consider narrowing the search pattern or directory scope."
	case strings.Contains(toolName, "lsp"):
		hint = "LSP may be indexing — subsequent calls should be faster."
	default:
		hint = "consider narrowing the operation scope."
	}

	return fmt.Sprintf(
		"Performance note: %s took %.1fs — %.0fx slower than its average (%.2fs). %s",
		toolName, dur.Seconds(), ratio, mean.Seconds(), hint,
	)
}

func (lt *LatencyTracker) meanLatency(toolName string) time.Duration {
	if lt == nil {
		return 0
	}
	lt.mu.Lock()
	defer lt.mu.Unlock()
	samples := lt.samples[toolName]
	if len(samples) == 0 {
		return 0
	}
	var total time.Duration
	for _, s := range samples {
		total += s.dur
	}
	return total / time.Duration(len(samples))
}

func (lt *LatencyTracker) sampleCount(toolName string) int {
	if lt == nil {
		return 0
	}
	lt.mu.Lock()
	defer lt.mu.Unlock()
	return len(lt.samples[toolName])
}
