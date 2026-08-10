package agent

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
)

// oodDetector detects Out-of-Distribution (OOD) situations where the agent
// encounters novel inputs, tool combinations, or workspace states outside
// its historical experience. This is a safety mechanism to prevent
// autonomous operation in unfamiliar contexts.
//
// Research basis: arXiv:2510.21254 "Out-of-Distribution Detection for
// Safety Assurance of AI and Autonomous Systems" (2025)
type oodDetector struct {
	mu sync.RWMutex

	// Feature statistics built from historical observations
	seenFileExts      map[string]int // .go, .ts, .md, etc.
	seenToolCombos    map[string]int // tool1+tool2 pairs
	seenWorkspaceRoot string         // last known workspace root
	sessionCount      int            // number of tool calls this session
	prevTool          string         // previous tool name (for combo tracking)

	// Configuration
	minSamples     int     // samples before OOD detection active (default 50)
	maxFileExts    int     // max distinct extensions to track (default 200)
	maxToolCombos  int     // max tool combos to track (default 500)
	alertThreshold float64 // novelty threshold (0.0-1.0, default 0.8)
}

// oodSignal represents a detected OOD situation
type oodSignal struct {
	Severity  ActionSeverity
	Message   string
	Features  []string // novel features detected
	Certainty float64  // 0.0-1.0
}

// newOODDetector creates an OOD detector with default thresholds
func newOODDetector() *oodDetector {
	return &oodDetector{
		seenFileExts:   make(map[string]int),
		seenToolCombos: make(map[string]int),
		minSamples:     50,
		maxFileExts:    200,
		maxToolCombos:  500,
		alertThreshold: 0.8,
	}
}

// recordObservation adds a tool call observation to the feature set
func (d *oodDetector) recordObservation(toolName string, targets []string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.sessionCount++

	// Extract file extensions from target paths
	for _, path := range targets {
		if ext := extractFileExt(path); ext != "" {
			d.seenFileExts[ext]++
		}
	}

	// Record tool combination with previous tool
	if d.prevTool != "" {
		combo := d.prevTool + "+" + toolName
		d.seenToolCombos[combo]++
	}
	d.prevTool = toolName

	// Prune to keep maps bounded
	if len(d.seenFileExts) > d.maxFileExts {
		d.pruneMap(d.seenFileExts, d.maxFileExts)
	}
	if len(d.seenToolCombos) > d.maxToolCombos {
		d.pruneMap(d.seenToolCombos, d.maxToolCombos)
	}
}

var prevTool string

// checkOOD evaluates if current tool call represents an OOD situation
func (d *oodDetector) checkOOD(toolName string, targets []string) *oodSignal {
	d.mu.RLock()
	defer d.mu.RUnlock()

	// Not enough data yet - not OOD, just learning
	if d.sessionCount < d.minSamples {
		return nil
	}

	var novelFeatures []string
	certainty := 0.0

	// Check for novel file extensions
	for _, path := range targets {
		if ext := extractFileExt(path); ext != "" {
			if d.seenFileExts[ext] == 0 {
				novelFeatures = append(novelFeatures, fmt.Sprintf("unseen file type: %s", ext))
				certainty += 0.3
			}
		}
	}

	// Check for novel tool combination
	if d.prevTool != "" {
		combo := d.prevTool + "+" + toolName
		if d.seenToolCombos[combo] == 0 {
			novelFeatures = append(novelFeatures, fmt.Sprintf("novel tool combo: %s → %s", d.prevTool, toolName))
			certainty += 0.4
		}
	}

	// Cap certainty
	if certainty > 1.0 {
		certainty = 1.0
	}

	// Only alert if threshold exceeded and have novel features
	if certainty >= d.alertThreshold && len(novelFeatures) > 0 {
		severity := SeverityMedium
		if certainty > 0.9 {
			severity = SeverityHigh
		}

		return &oodSignal{
			Severity:  severity,
			Message:   "Out-of-distribution: encountering novel patterns outside historical experience",
			Features:  novelFeatures,
			Certainty: certainty,
		}
	}

	return nil
}

// pruneMap keeps only the top-k entries by count
func (d *oodDetector) pruneMap(m map[string]int, k int) {
	type entry struct {
		key   string
		count int
	}

	var entries []entry
	for k, v := range m {
		entries = append(entries, entry{k, v})
	}

	slices.SortFunc(entries, func(a, b entry) int {
		return b.count - a.count // descending
	})

	// Clear and re-populate with top-k
	clear(m)
	for i, e := range entries {
		if i >= k {
			break
		}
		m[e.key] = e.count
	}
}

// extractFileExt extracts the file extension from a path
// Handles compound extensions (.tar.gz) and query strings
func extractFileExt(path string) string {
	// Remove query string if present
	if idx := strings.Index(path, "?"); idx != -1 {
		path = path[:idx]
	}

	// Handle common compound extensions before LastIndex
	if strings.HasSuffix(path, ".tar.gz") {
		return ".tar.gz"
	}
	if strings.HasSuffix(path, ".tar.bz2") {
		return ".tar.bz2"
	}

	// Find last dot
	idx := strings.LastIndex(path, ".")
	if idx == -1 || idx == len(path)-1 {
		return ""
	}

	// Reject hidden files (dot at start of filename)
	// Check if there's a path separator before the dot
	lastSlash := strings.LastIndexAny(path, "/\\")
	if idx == 0 || (lastSlash != -1 && idx == lastSlash+1) {
		return ""
	}

	ext := path[idx:]
	// Validate extension (alphanumeric only)
	for _, ch := range ext[1:] {
		if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9')) {
			return ""
		}
	}

	return ext
}

// copyStats returns a copy of current statistics for testing
func (d *oodDetector) copyStats() (map[string]int, map[string]int) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return maps.Clone(d.seenFileExts), maps.Clone(d.seenToolCombos)
}

// addObservations is a test helper to pre-populate statistics
func (d *oodDetector) addObservations(fileExts, toolCombos []string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	for _, ext := range fileExts {
		d.seenFileExts[ext]++
	}

	for _, combo := range toolCombos {
		d.seenToolCombos[combo]++
	}

	d.sessionCount += len(fileExts) + len(toolCombos)
}
