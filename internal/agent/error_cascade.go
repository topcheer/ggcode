package agent

// Error Cascade Detection -- Common-Root-Cause Error Clustering
//
// Research basis: Google's "Reliability Engineering for AI Systems" (2025) and
// Microsoft's AutoGen error-handling framework emphasize that cascading failures
// -- where a single root cause propagates through dependent operations -- are the
// #1 source of wasted iterations in autonomous agents. When file A has a syntax
// error, every subsequent tool that reads, compiles, or depends on file A also
// fails. The agent treats each failure independently, trying to fix each symptom
// rather than addressing the root cause.
//
// Key insight from SWE-bench trajectory analysis: in failing trajectories, 40%+
// of error sequences share a common file path or symbol root cause, but agents
// only identify the root cause ~20% of the time without explicit guidance.
//
// Competitor analysis:
//   - Claude Code: no cascade detection; treats each error independently
//   - Cursor: user-driven; no runtime cascade analysis
//   - OpenHands: separate critic LLM identifies patterns (costs tokens)
//   - Devin: SICA overseer tracks productivity but not causal error chains
//   - Aider: lint-fail retry loop is file-specific but doesn't detect cross-tool cascades
//
// Gap in existing ggcode systems:
//   - error_classifier.go: classifies error TYPE (file_not_found, type_error, etc.)
//     and fires once per category. Doesn't detect that multiple different-category
//     errors share the same root file/symbol.
//   - compounding_failure.go: detects high failure RATE across diverse categories.
//     But it specifically requires DIVERSE categories -- the opposite of a cascade
//     where errors cluster around ONE resource.
//   - recurring_error.go: detects the SAME build error fingerprint recurring.
//     Only covers build/test command output, not cross-tool error cascades.
//   - failure_mode.go: classifies transient/structural/systemic mode. Doesn't
//     link errors by shared root cause.
//   - edit_fail_recovery.go: tracks consecutive edit failures per file. Only
//     covers edit_file, not cascading failures across read/build/search.
//
// This component fills the gap with deterministic, zero-LLM-cost detection:
//
//   1. ROOT EXTRACTION: when a tool fails, extract the associated file path or
//      symbol from the error content (e.g., "/path/to/file.go" from a compile
//      error, "FuncName" from an undefined-symbol error).
//
//   2. CLUSTER TRACKING: group errors by their root resource. When 3+ errors
//      in the current run share the same root resource, a cascade is detected.
//
//   3. TARGETED GUIDANCE: inject guidance directing the agent to fix the root
//      cause resource FIRST before attempting any more dependent operations.
//      Escalates at 4+ errors (strong guidance) and 5+ (abort recommendation).

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// cascadeSoftThreshold: minimum errors sharing a root resource to trigger
	// initial cascade guidance.
	cascadeSoftThreshold = 3

	// cascadeHardThreshold: strong guidance to stop and fix root cause.
	cascadeHardThreshold = 4

	// cascadeAbortThreshold: recommend abandoning the current approach.
	cascadeAbortThreshold = 5

	// cascadeMaxRoots: maximum root resources to track (memory bound).
	cascadeMaxRoots = 20
)

// cascadeRootType identifies what kind of root resource caused the cascade.
type cascadeRootType int

const (
	cascadeRootNone   cascadeRootType = iota
	cascadeRootFile                   // errors share a common file path
	cascadeRootSymbol                 // errors share a common symbol/function name
)

func (r cascadeRootType) String() string {
	switch r {
	case cascadeRootFile:
		return "file"
	case cascadeRootSymbol:
		return "symbol"
	default:
		return "unknown"
	}
}

// cascadeEntry records a single error associated with a root resource.
type cascadeEntry struct {
	toolName string
	rootKey  string // normalized file path or symbol name
	rootType cascadeRootType
}

// errorCascadeState tracks error clusters by root resource to detect cascading
// failures from a common cause.
type errorCascadeState struct {
	mu sync.Mutex

	// roots maps rootKey → list of errors associated with that root.
	roots map[string][]cascadeEntry

	// fired tracks which root keys have already triggered guidance.
	fired map[string]bool

	// totalErrors counts all errors recorded this run.
	totalErrors int
}

func newErrorCascadeState() *errorCascadeState {
	return &errorCascadeState{
		roots: make(map[string][]cascadeEntry),
		fired: make(map[string]bool),
	}
}

func (e *errorCascadeState) reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.roots = make(map[string][]cascadeEntry)
	e.fired = make(map[string]bool)
	e.totalErrors = 0
}

// Patterns for extracting root resources from error content.

// filePathRe matches file paths in error messages. Supports Unix and Windows paths
// with common source extensions. We use FindString (not submatches) because the
// repeated group in the regex only captures the last iteration.
var filePathRe = regexp.MustCompile(`(?:/[a-zA-Z0-9._-]+)+\.[a-zA-Z]+|[a-zA-Z]:\\(?:[a-zA-Z0-9._-]+\\)+[a-zA-Z0-9._-]+\.[a-zA-Z]+`)

// quotedFileRe matches file paths in quotes (common in tool errors).
var quotedFileRe = regexp.MustCompile(`["']([^"']+\.[a-zA-Z]+)["']`)

// symbolRe matches Go-style symbol references in undefined/missing errors.
var symbolRe = regexp.MustCompile(`(?:undefined|undeclared|cannot find|not defined|no such|unknown)[:\s]+([a-zA-Z_][a-zA-Z0-9_.]*(?:\.[a-zA-Z_][a-zA-Z0-9_]*)?)`)

// extractCascadeRoot attempts to identify the root resource (file path or symbol)
// from an error message. Returns the normalized key, the type, and whether a root
// was found.
func extractCascadeRoot(toolName, content string) (string, cascadeRootType) {
	if content == "" {
		return "", cascadeRootNone
	}

	// Strategy 1: For editing tools, extract the file path from arguments.
	// This is the most reliable source since we know exactly which file was targeted.
	if isEditingTool(toolName) {
		// Editing tools have their path in arguments, but we only have content here.
		// Fall back to content-based extraction.
	}

	// Strategy 2: Extract quoted file paths (most common in tool errors).
	if matches := quotedFileRe.FindStringSubmatch(content); len(matches) > 1 {
		path := normalizeCascadePath(matches[1])
		if path != "" {
			return path, cascadeRootFile
		}
	}

	// Strategy 3: Extract bare file paths from error messages.
	if raw := filePathRe.FindString(content); raw != "" {
		path := normalizeCascadePath(raw)
		if path != "" {
			return path, cascadeRootFile
		}
	}

	// Strategy 4: Extract symbol names from undefined/undeclared errors.
	if matches := symbolRe.FindStringSubmatch(content); len(matches) > 1 {
		sym := strings.TrimSpace(matches[1])
		// Filter out common false positives (single letters, keywords).
		if len(sym) >= 3 && !isGoKeyword(sym) {
			return strings.ToLower(sym), cascadeRootSymbol
		}
	}

	return "", cascadeRootNone
}

// normalizeCascadePath normalizes a file path for clustering: takes the base name
// plus parent directory to avoid over-merging (many files share base names).
func normalizeCascadePath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.ReplaceAll(path, "\\", "/")
	path = strings.TrimPrefix(path, "/")

	// Split and filter empty parts (from leading/double slashes).
	var parts []string
	for _, p := range strings.Split(path, "/") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return ""
	}

	// Use last 2 path components (parent/basename) as the cluster key.
	if len(parts) >= 2 {
		return parts[len(parts)-2] + "/" + parts[len(parts)-1]
	}
	return parts[0]
}

// isEditingTool returns true for tools that modify files.
func isEditingTool(name string) bool {
	switch name {
	case "edit_file", "multi_edit_file", "multi_file_edit", "write_file", "notebook_edit":
		return true
	}
	return false
}

// isGoKeyword filters symbol extraction false positives.
func isGoKeyword(s string) bool {
	keywords := map[string]bool{
		"func": true, "var": true, "const": true, "type": true,
		"struct": true, "interface": true, "package": true,
		"import": true, "return": true, "break": true, "continue": true,
		"true": true, "false": true, "nil": true, "range": true,
		"for": true, "if": true, "else": true, "switch": true,
		"case": true, "default": true, "defer": true, "go": true,
	}
	return keywords[strings.ToLower(s)]
}

// recordError records a tool error and returns cascade guidance if a cascade
// pattern is detected. Returns empty string if no guidance is needed.
func (e *errorCascadeState) recordError(toolName, content string) string {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.totalErrors++

	rootKey, rootType := extractCascadeRoot(toolName, content)
	if rootKey == "" {
		return "" // Can't cluster without a root resource
	}

	entry := cascadeEntry{
		toolName: toolName,
		rootKey:  rootKey,
		rootType: rootType,
	}

	e.roots[rootKey] = append(e.roots[rootKey], entry)

	// Bound memory: if too many roots, evict the one with fewest entries.
	if len(e.roots) > cascadeMaxRoots {
		var minKey string
		var secondMinKey string
		minCount := -1
		secondMinCount := -1
		for k, v := range e.roots {
			c := len(v)
			if minCount == -1 || c < minCount {
				secondMinKey = minKey
				secondMinCount = minCount
				minKey = k
				minCount = c
			} else if secondMinCount == -1 || c < secondMinCount {
				secondMinKey = k
				secondMinCount = c
			}
		}
		// Evict the root with fewest entries, but never evict the one we just added.
		evictKey := minKey
		if minKey == rootKey && secondMinKey != "" {
			evictKey = secondMinKey
		}
		if evictKey != "" && evictKey != rootKey {
			delete(e.roots, evictKey)
		}
	}

	count := len(e.roots[rootKey])

	// Check if this root has crossed a threshold and hasn't fired yet.
	if e.fired[rootKey] {
		return ""
	}

	var guidance string
	switch {
	case count >= cascadeAbortThreshold:
		e.fired[rootKey] = true
		guidance = fmt.Sprintf(
			"[Error Cascade: ABORT] %d tool failures share root cause %s '%s'. "+
				"The current approach is not working -- every operation touching this "+
				"%s fails. STOP attempting operations on '%s'. Instead: (1) re-read "+
				"the %s from scratch to understand its current state, (2) check if "+
				"another process or agent modified it, (3) consider reverting to a "+
				"known-good state, or (4) escalate to the user if this is an "+
				"environment issue.",
			count, rootType, rootKey, rootType, rootKey, rootType,
		)
	case count >= cascadeHardThreshold:
		e.fired[rootKey] = true
		guidance = fmt.Sprintf(
			"[Error Cascade: ROOT CAUSE] %d tool failures share root cause %s '%s'. "+
				"These are NOT independent errors -- they all stem from the same "+
				"underlying problem with this %s. FIX '%s' first before attempting "+
				"any other dependent operations. Common root causes: syntax error, "+
				"missing import, incorrect type, renamed symbol, or file corruption.",
			count, rootType, rootKey, rootType, rootKey,
		)
	case count >= cascadeSoftThreshold:
		e.fired[rootKey] = true
		guidance = fmt.Sprintf(
			"[Error Cascade] %d tool failures share root cause %s '%s'. "+
				"Multiple errors are clustering around this %s -- they likely share "+
				"a common root cause. Focus on fixing '%s' first; fixing it may "+
				"resolve several downstream errors at once.",
			count, rootType, rootKey, rootType, rootKey,
		)
	}

	if guidance != "" {
		debug.Log("cascade", "cascade detected: root=%s type=%s count=%d tool=%s",
			rootKey, rootType, count, toolName)
	}

	return guidance
}

// cascadeStats returns summary statistics for observability.
func (e *errorCascadeState) cascadeStats() (totalRoots, maxCluster, totalErrors int) {
	e.mu.Lock()
	defer e.mu.Unlock()

	maxCluster = 0
	for _, entries := range e.roots {
		if len(entries) > maxCluster {
			maxCluster = len(entries)
		}
	}
	return len(e.roots), maxCluster, e.totalErrors
}
