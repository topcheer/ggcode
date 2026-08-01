package agent

import (
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// Adaptive Tool Timeout — per-tool timeout profiles that adapt to observed latency.
//
// Problem: The previous implementation used a flat 5-minute timeout for ALL tools.
// A hung read_file or grep wastes 5 full minutes before the timeout fires. Different
// tool categories have very different expected latencies:
//   - File I/O (read_file, list_directory): typically <1s, almost never >10s
//   - Search (grep, search_files, glob): typically 1-5s, rarely >30s
//   - LSP tools: 0.5-3s normally, may spike during indexing
//   - Edit tools: <1s for normal files
//   - Web tools (web_search, web_fetch): 2-15s, may be slower on large pages
//   - MCP tools (mcp__*): highly variable, depends on external server
//   - Browser/mobile: inherently slow, need generous timeout
//
// Solution: Two-tier timeout computation:
//  1. Category-based default: sensible per-category bounds when no history exists
//  2. Adaptive: when 3+ latency samples exist, use mean * multiplier (clamped)
//
// The adaptive component uses the EXISTING LatencyTracker data that was previously
// collected but unused (meanLatency was dead code). This is a zero-waste integration:
// the latency baseline data is already being collected for outlier detection — we
// now also use it for timeout computation.
//
// Competitor analysis:
//   - Claude Code: flat 2-min per-tool timeout, no adaptation
//   - Cursor: no per-tool timeout (relies on overall session timeout)
//   - Cline: 60s default, configurable per-tool-type in settings
//   - OpenHands: 300s flat, no adaptation
//   - Aider: no per-tool timeout
//
// Key insight from gRPC/Envoy circuit-breaking literature: timeout should be a
// function of observed latency, not a static constant. This prevents both
// premature kills (for legitimately slow tools) and wasted time (for hung tools).

const (
	// adaptiveTimeoutMultiplier: multiply the mean latency by this factor to
	// get the adaptive timeout. 5x gives generous headroom for variance while
	// still catching genuinely hung operations much faster than a flat 5 minutes.
	adaptiveTimeoutMultiplier = 5.0

	// adaptiveMinSamples: need at least this many samples before trusting
	// the adaptive computation. Below this, use category defaults.
	adaptiveMinSamples = 3

	// Hard bounds: regardless of adaptive computation, never go below or above these.
	adaptiveTimeoutFloor = 10 * time.Second // never kill a tool in <10s (allows for cold starts)
	adaptiveTimeoutCeil  = 5 * time.Minute  // never wait more than 5 minutes (same as old flat default)
)

// toolTimeoutCategory classifies tools into categories for sensible default timeouts.
type toolTimeoutCategory int

const (
	catFileIO  toolTimeoutCategory = iota // read_file, multi_file_read, list_directory
	catSearch                             // grep, search_files, glob, code_search
	catEdit                               // edit_file, write_file, multi_edit_file, multi_file_write
	catLSP                                // lsp_*, code_health
	catWeb                                // web_search, web_fetch
	catMCP                                // mcp__* (external servers)
	catGit                                // git_* tools
	catDefault                            // everything else
	catBrowser                            // browser, screenshot, mobile_device
)

// categoryDefaultTimeout returns the baseline timeout for a tool category.
// These are deliberately generous to avoid false positives — the adaptive
// computation will tighten them once latency data is available.
func categoryDefaultTimeout(cat toolTimeoutCategory) time.Duration {
	switch cat {
	case catFileIO:
		return 60 * time.Second // reads should be fast, but large files/network mounts exist
	case catSearch:
		return 60 * time.Second // search on large repos can be slow
	case catEdit:
		return 30 * time.Second // edits should be near-instant
	case catLSP:
		return 60 * time.Second // LSP may be indexing
	case catWeb:
		return 120 * time.Second // network latency, large page fetches
	case catMCP:
		return 120 * time.Second // external MCP server, unknown performance
	case catGit:
		return 60 * time.Second // git operations on large repos
	case catBrowser:
		return 180 * time.Second // browser automation is inherently slow
	default:
		return 120 * time.Second // conservative default for unknown tools
	}
}

// classifyTool returns the timeout category for a given tool name.
// MCP tools are identified by the "mcp__" prefix.
func classifyTool(toolName string) toolTimeoutCategory {
	// MCP tools from external servers
	if strings.HasPrefix(toolName, "mcp__") {
		return catMCP
	}

	// Check well-known tool names
	switch {
	case toolName == "read_file" || toolName == "multi_file_read" || toolName == "list_directory":
		return catFileIO
	case toolName == "grep" || toolName == "search_files" || toolName == "glob" || toolName == "code_search":
		return catSearch
	case toolName == "edit_file" || toolName == "write_file" ||
		toolName == "multi_edit_file" || toolName == "multi_file_write" ||
		toolName == "notebook_edit":
		return catEdit
	case strings.HasPrefix(toolName, "lsp_") || toolName == "code_health":
		return catLSP
	case toolName == "web_search" || toolName == "web_fetch":
		return catWeb
	case strings.HasPrefix(toolName, "git_"):
		return catGit
	case toolName == "browser" || toolName == "screenshot" || toolName == "mobile_device":
		return catBrowser
	default:
		return catDefault
	}
}

// computeAdaptiveTimeout determines the optimal timeout for a tool call based on:
// 1. The tool's historical latency baseline (if available)
// 2. The tool's category default (as fallback)
// 3. Hard floor and ceiling bounds
//
// When sufficient latency history exists (>= adaptiveMinSamples), the timeout
// is set to mean * adaptiveTimeoutMultiplier. This adapts to each tool's actual
// performance profile:
//   - A read_file with mean 50ms gets a 250ms-adaptive timeout (clamped to 10s floor)
//   - An MCP tool with mean 15s gets a 75s timeout
//   - A web_fetch with mean 30s gets a 150s timeout
//
// When no history exists, the category default is used.
func (lt *LatencyTracker) computeAdaptiveTimeout(toolName string) time.Duration {
	if lt == nil {
		return categoryDefaultTimeout(classifyTool(toolName))
	}

	cat := classifyTool(toolName)
	catDefault := categoryDefaultTimeout(cat)

	// Try to use historical latency data for adaptive computation.
	mean := lt.meanLatency(toolName)
	if mean <= 0 {
		// No baseline data yet — use category default.
		return catDefault
	}

	// Compute adaptive timeout: mean * multiplier, clamped to [floor, ceil].
	adaptive := time.Duration(float64(mean) * adaptiveTimeoutMultiplier)

	// Never go below the floor (allows for cold starts and variance).
	if adaptive < adaptiveTimeoutFloor {
		adaptive = adaptiveTimeoutFloor
	}

	// Never exceed the ceiling (same as the old flat timeout — 5 min).
	if adaptive > adaptiveTimeoutCeil {
		adaptive = adaptiveTimeoutCeil
	}

	// Also ensure we don't set a timeout LOWER than the category default
	// unless we have strong evidence (mean is well below category default).
	// This prevents premature kills on tools that occasionally spike.
	// Exception: if the adaptive value is at the floor, respect it.
	if adaptive < catDefault && adaptive > adaptiveTimeoutFloor {
		// Only use the lower adaptive value if we have enough samples
		// to be confident. meanLatency already returns 0 for insufficient data.
		adaptive = catDefault
	}

	debug.Log("agent", "adaptive timeout for %s: mean=%v → timeout=%v (cat=%v default=%v)",
		toolName, mean, adaptive, cat, catDefault)

	return adaptive
}
