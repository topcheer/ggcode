package agent

// Context Footprint Tracker - Per-Tool Context Budget Attribution
//
// Research basis: Context Engineering (Anthropic, 2025) and ACE benchmark
// (ICLR 2026) show that tool results are the #1 source of context bloat in
// coding agents. 40-60% of consumed context comes from tool outputs, not
// conversation. Yet most agents have zero visibility into WHICH tools are
// consuming the most context, making optimization impossible.
//
// This tracker provides that visibility. It estimates the token footprint of
// each tool's results and accumulates per-tool totals. When a single tool
// category dominates the context budget, it injects actionable guidance:
//
//   - Search tools (grep, search_files): suggest narrower patterns
//   - Read tools (read_file, multi_file_read): suggest offset/limit
//   - Command tools (run_command, etc.): suggest piping to files
//
// This is different from:
//   - tool_output_guard: truncates individual oversized results (per-call)
//   - arg_size_guard: detects oversized tool arguments (input side)
//   - cost_budget: tracks absolute session token spending (LLM call side)
//   - cost_budget: enforces absolute session token limit (LLM tokens)
//
// This tracks the OUTPUT side across the full session: which tools are
// cumulatively consuming the most context window space, and intervenes
// when a category exceeds a dominance threshold.

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// footprintEstimatePerChar is a rough token estimation ratio.
	// Production-quality estimation uses the context manager's tokenizer,
	// but for per-tool attribution, this heuristic is sufficient.
	footprintEstimatePerChar = 0.27 // ~3.7 chars per token average for code

	// footprintDominanceThreshold: when a single tool category accounts for
	// this fraction of total tool-result context, inject guidance.
	footprintDominanceThreshold = 0.40

	// footprintMinTotalTokens: minimum total tool-result tokens before
	// dominance checking activates (avoids noise in short runs).
	footprintMinTotalTokens = 8000

	// footprintCooldown prevents repeated guidance within this window.
	footprintCooldown = 5 * time.Minute

	// footprintWarnOncePerCategory: each category fires guidance at most once.
	// After the first warning, the agent has been informed.
)

// footprintCategory classifies tools into groups for footprint analysis.
type footprintCategory string

const (
	footprintCatSearch  footprintCategory = "search"
	footprintCatRead    footprintCategory = "read"
	footprintCatCommand footprintCategory = "command"
	footprintCatLSP     footprintCategory = "lsp"
	footprintCatOther   footprintCategory = "other"
)

// classifyFootprintTool returns the footprint category for a tool.
func classifyFootprintTool(name string) footprintCategory {
	switch name {
	case "grep", "search_files", "code_search", "glob":
		return footprintCatSearch
	case "read_file", "multi_file_read", "list_directory":
		return footprintCatRead
	case "run_command", "start_command", "read_command_output", "wait_command":
		return footprintCatCommand
	case "lsp_symbols", "lsp_definition", "lsp_references", "lsp_hover",
		"lsp_workspace_symbols", "lsp_document_highlights",
		"lsp_implementation", "lsp_diagnostics", "lsp_code_actions":
		return footprintCatLSP
	default:
		return footprintCatOther
	}
}

// categoryLabel returns a human-readable label for guidance messages.
func (c footprintCategory) label() string {
	switch c {
	case footprintCatSearch:
		return "Search/grep"
	case footprintCatRead:
		return "File reads"
	case footprintCatCommand:
		return "Shell commands"
	case footprintCatLSP:
		return "LSP queries"
	default:
		return "Tool results"
	}
}

// categoryHint returns actionable guidance for reducing context from this category.
func (c footprintCategory) hint(totalTokens int) string {
	pct := float64(totalTokens) * footprintEstimatePerChar
	_ = pct // used in message below
	switch c {
	case footprintCatSearch:
		return "Use more specific regex patterns, narrow the directory scope, or use glob for file discovery first. Consider piping large grep output to a file and reading selectively."
	case footprintCatRead:
		return "Use offset/limit parameters to read only the relevant sections. Use multi_file_read to batch small reads instead of individual read_file calls."
	case footprintCatCommand:
		return "Pipe verbose command output to a file and read only the relevant lines. Use tail/head to limit output. Consider reducing -parallel or limiting result counts."
	case footprintCatLSP:
		return "Use lsp_workspace_symbols with a specific query instead of broad symbol dumps. Prefer lsp_definition/references over reading entire symbol tables."
	default:
		return "Batch related operations and use targeted parameters to reduce result size."
	}
}

// footprintEntry tracks a single tool result's estimated size.
type footprintEntry struct {
	tool     string
	category footprintCategory
	tokens   int
	iter     int
}

// contextFootprintState accumulates per-tool and per-category token estimates
// across a session, detecting when a category dominates the context budget.
type contextFootprintState struct {
	mu             sync.Mutex
	entries        []footprintEntry
	categoryTotals map[footprintCategory]int
	toolTotals     map[string]int
	totalTokens    int
	warned         map[footprintCategory]bool // categories that already fired guidance
	lastWarn       time.Time
}

func newContextFootprintState() *contextFootprintState {
	return &contextFootprintState{
		categoryTotals: make(map[footprintCategory]int),
		toolTotals:     make(map[string]int),
		warned:         make(map[footprintCategory]bool),
	}
}

// reset clears all accumulated state for a new run.
func (f *contextFootprintState) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = f.entries[:0]
	f.categoryTotals = make(map[footprintCategory]int)
	f.toolTotals = make(map[string]int)
	f.totalTokens = 0
	f.warned = make(map[footprintCategory]bool)
	f.lastWarn = time.Time{}
}

// recordResult estimates the token footprint of a tool result and accumulates it.
func (f *contextFootprintState) recordResult(toolName, resultContent string, iter int) {
	cat := classifyFootprintTool(toolName)
	tokens := estimateResultTokens(resultContent)

	f.mu.Lock()
	defer f.mu.Unlock()

	f.entries = append(f.entries, footprintEntry{
		tool:     toolName,
		category: cat,
		tokens:   tokens,
		iter:     iter,
	})
	f.categoryTotals[cat] += tokens
	f.toolTotals[toolName] += tokens
	f.totalTokens += tokens
}

// estimateResultTokens provides a fast character-based token estimate.
func estimateResultTokens(content string) int {
	if len(content) == 0 {
		return 0
	}
	return int(float64(len(content)) * footprintEstimatePerChar)
}

// check evaluates whether any tool category dominates the context budget
// and returns guidance if so. Returns empty string if no action needed.
func (f *contextFootprintState) check() string {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Skip if total tool results are too small to matter.
	if f.totalTokens < footprintMinTotalTokens {
		return ""
	}

	now := time.Now()
	if now.Sub(f.lastWarn) < footprintCooldown {
		return ""
	}

	// Find the dominant category.
	var dominantCat footprintCategory
	var dominantTokens int
	for cat, tokens := range f.categoryTotals {
		if tokens > dominantTokens {
			dominantTokens = tokens
			dominantCat = cat
		}
	}

	// Check dominance threshold.
	pct := float64(dominantTokens) / float64(f.totalTokens)
	if pct < footprintDominanceThreshold {
		return ""
	}

	// Skip if already warned about this category.
	if f.warned[dominantCat] {
		return ""
	}

	f.warned[dominantCat] = true
	f.lastWarn = now

	// Build top tools in this category for actionable detail.
	type toolStat struct {
		name   string
		tokens int
	}
	var stats []toolStat
	for name, tokens := range f.toolTotals {
		if classifyFootprintTool(name) == dominantCat {
			stats = append(stats, toolStat{name, tokens})
		}
	}
	sort.Slice(stats, func(i, j int) bool { return stats[i].tokens > stats[j].tokens })

	var topTools []string
	for i, s := range stats {
		if i >= 3 {
			break
		}
		topTools = append(topTools, fmt.Sprintf("%s (%s)", s.name, formatTokenCount(int64(s.tokens))))
	}

	debug.Log("context-footprint", "%s category dominates: %s/%s tokens (%.0f%%), top: %s",
		dominantCat.label(), formatTokenCount(int64(dominantTokens)),
		formatTokenCount(int64(f.totalTokens)), pct*100, strings.Join(topTools, ", "))

	msg := fmt.Sprintf(
		"[context footprint] %s dominate tool-result context: %s of %s total (%.0f%%). Top: %s. %s",
		dominantCat.label(),
		formatTokenCount(int64(dominantTokens)),
		formatTokenCount(int64(f.totalTokens)),
		pct*100,
		strings.Join(topTools, ", "),
		dominantCat.hint(dominantTokens),
	)

	return msg
}

// summary returns a compact per-category breakdown for observability.
func (f *contextFootprintState) summary() string {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.totalTokens == 0 {
		return ""
	}

	type catStat struct {
		cat    footprintCategory
		tokens int
	}
	var stats []catStat
	for cat, tokens := range f.categoryTotals {
		stats = append(stats, catStat{cat, tokens})
	}
	sort.Slice(stats, func(i, j int) bool { return stats[i].tokens > stats[j].tokens })

	var parts []string
	for _, s := range stats {
		pct := float64(s.tokens) / float64(f.totalTokens) * 100
		parts = append(parts, fmt.Sprintf("%s: %s (%.0f%%)", s.cat.label(), formatTokenCount(int64(s.tokens)), pct))
	}
	return strings.Join(parts, ", ")
}
