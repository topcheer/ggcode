package agent

import (
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	adaptiveTimeoutMultiplier = 5.0

	adaptiveMinSamples = 3

	adaptiveTimeoutFloor = 10 * time.Second
	adaptiveTimeoutCeil  = 5 * time.Minute
)

type toolTimeoutCategory int

const (
	catFileIO toolTimeoutCategory = iota
	catSearch
	catEdit
	catLSP
	catWeb
	catMCP
	catGit
	catDefault
	catBrowser
	catLLM
)

func categoryDefaultTimeout(cat toolTimeoutCategory) time.Duration {
	switch cat {
	case catFileIO:
		return 60 * time.Second
	case catSearch:
		return 60 * time.Second
	case catEdit:
		return 30 * time.Second
	case catLSP:
		return 60 * time.Second
	case catWeb:
		return 120 * time.Second
	case catMCP:
		return 120 * time.Second
	case catGit:
		return 60 * time.Second
	case catBrowser:
		return 180 * time.Second
	case catLLM:
		// One-shot LLM calls (llm_call) run non-streaming and inherit the
		// session reasoning effort; thinking models routinely take minutes
		// server-side before the full response returns. 2m (catDefault)
		// killed most real calls (#5286-class user report 2026-11-08).
		return 10 * time.Minute
	default:
		return 120 * time.Second
	}
}

var timeoutCategoryByTool = map[string]toolTimeoutCategory{

	"read_file":       catFileIO,
	"multi_file_read": catFileIO,
	"list_directory":  catFileIO,

	"grep":         catSearch,
	"search_files": catSearch,
	"glob":         catSearch,
	"code_search":  catSearch,

	"edit_file":        catEdit,
	"write_file":       catEdit,
	"multi_edit_file":  catEdit,
	"multi_file_edit":  catEdit,
	"multi_file_write": catEdit,
	"batch_replace":    catEdit,
	"lsp_rename":       catEdit,
	"file_ops":         catEdit,
	"notebook_edit":    catEdit,

	"code_health": catLSP,

	"web_search": catWeb,
	"web_fetch":  catWeb,

	"browser":       catBrowser,
	"screenshot":    catBrowser,
	"mobile_device": catBrowser,

	"llm_call": catLLM,
}

func classifyTool(toolName string) toolTimeoutCategory {
	if strings.HasPrefix(toolName, "mcp__") {
		return catMCP
	}
	if cat, ok := timeoutCategoryByTool[toolName]; ok {
		return cat
	}
	switch {
	case strings.HasPrefix(toolName, "lsp_"):
		return catLSP
	case strings.HasPrefix(toolName, "git_"):
		return catGit
	default:
		return catDefault
	}
}

func (lt *LatencyTracker) computeAdaptiveTimeout(toolName string) time.Duration {
	if lt == nil {
		return categoryDefaultTimeout(classifyTool(toolName))
	}

	cat := classifyTool(toolName)
	catDefault := categoryDefaultTimeout(cat)

	// LLM one-shot latency is high-variance (model queue, thinking depth,
	// schema re-ask loops); adaptive shrinkage must never cut it below the
	// category default — a fast history does not mean the next call is fast.
	if cat == catLLM {
		return catDefault
	}

	if lt.sampleCount(toolName) < adaptiveMinSamples {
		return catDefault
	}

	mean := lt.meanLatency(toolName)
	if mean <= 0 {

		return catDefault
	}

	adaptive := time.Duration(float64(mean) * adaptiveTimeoutMultiplier)

	if adaptive < adaptiveTimeoutFloor {
		adaptive = adaptiveTimeoutFloor
	}

	if adaptive > adaptiveTimeoutCeil {
		adaptive = adaptiveTimeoutCeil
	}

	if adaptive < catDefault && adaptive > adaptiveTimeoutFloor {
		adaptive = catDefault
	}

	debug.Log("agent", "adaptive timeout for %s: mean=%v → timeout=%v (cat=%v default=%v)",
		toolName, mean, adaptive, cat, catDefault)

	return adaptive
}
