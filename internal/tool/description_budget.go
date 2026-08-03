package tool

import (
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// Tool Description Token Budgeting.
//
// Research basis: The "Tool Schema Bloat" problem (RAG-MCP paper 2025,
// arXiv:2504.02449) found that MCP server tool descriptions are often
// excessively verbose — 500-3000+ characters each with usage examples, edge
// case notes, and multi-paragraph documentation. With 20-50 MCP tools loaded,
// tool schema alone can consume 5,000-15,000 tokens per API call, representing
// 5-10% of a 128K context window on EVERY turn — even turns where those tools
// are never called.
//
// Claude Code solves this with terse built-in descriptions and deferred MCP
// loading (tool names only, schemas fetched on-demand). Cursor compresses tool
// descriptions automatically. Aider minimizes tool count entirely.
//
// This module applies a per-tool description budget: descriptions exceeding
// maxDescriptionBytes are truncated to the first paragraph (or first
// maxDescriptionBytes), preserving the tool name and parameter schema intact.
// The truncation preserves the beginning of the description because the most
// important information (what the tool does) is always at the top.
//
// Key design decisions:
//   - Only external (MCP/plugin) tool descriptions are trimmed — built-in
//     tools have hand-tuned descriptions that should never be compressed.
//   - Truncation preserves the first paragraph (sentences ending before any
//     blank line), which captures the core purpose. If the first paragraph
//     itself is too long, it is hard-cut at maxDescriptionBytes.
//   - A "…" marker is appended so the LLM knows the description was shortened.
//   - Parameters schema is NEVER modified — only the description text.
//   - Zero-cost: pure string operations, no LLM calls.
//   - Activates per-call alongside RelevanceFilter — same invocation point.

const (
	// maxDescriptionBytes is the maximum allowed tool description length in
	// bytes before truncation. 500 bytes ≈ ~125 tokens, which keeps even 50
	// tools under ~6K tokens of description budget. This is generous enough
	// for a clear purpose statement + key behavior notes, but eliminates
	// multi-KB documentation dumps that waste context on every turn.
	maxDescriptionBytes = 500

	// minTrimLength avoids trimming descriptions that are only slightly over
	// the limit — the truncation marker would take more space than it saves.
	minTrimLength = 350

	// Adaptive budget levels based on context pressure (0.0–1.0).
	// As context fills up, descriptions are trimmed more aggressively to
	// free space for actual conversation content.
	//
	// Research basis: Claude Code and Cursor both implement context-pressure
	// aware optimizations. Claude Code trims system prompt sections dynamically;
	// Cursor compresses tool schemas under pressure. The key insight is that
	// tool descriptions are low-value when the model already knows the tool
	// from recent calls — the description was needed for discovery, not for
	// repeated use within the same session.
	//
	// Pressure tiers:
	//   < 0.50 (low):     no trimming (maxDescriptionBytes = 500)
	//   0.50–0.70 (moderate): standard trimming (500 bytes)
	//   0.70–0.85 (high): aggressive trimming (250 bytes)
	//   > 0.85 (critical): minimal descriptions (120 bytes)
	pressureTierModerate = 0.50
	pressureTierHigh     = 0.70
	pressureTierCritical = 0.85

	budgetAtModerate = 500 // standard budget
	budgetAtHigh     = 250 // aggressive: keep purpose + first param
	budgetAtCritical = 120 // minimal: purpose only
)

// trimToolDescriptions applies a description budget to tool definitions.
// Descriptions exceeding maxDescriptionBytes are truncated to the first
// paragraph, with a truncation marker appended.
//
// Built-in tools are exempt — their descriptions are carefully authored and
// provide essential context for correct tool selection. Only external (MCP/
// plugin) tools with verbose auto-generated descriptions are trimmed.
//
// This is called after relevance filtering so we only spend trimming effort
// on tools that will actually be sent to the LLM.
func trimToolDescriptions(defs []provider.ToolDefinition) []provider.ToolDefinition {
	return trimToolDescriptionsWithPressure(defs, 0)
}

// trimToolDescriptionsWithPressure applies context-pressure-aware adaptive
// description budgeting. As context utilization rises, the per-tool description
// budget shrinks progressively, freeing context tokens for actual conversation
// content.
//
// pressure is the context utilization ratio (0.0–1.0). A value of 0 or
// negative means no pressure information available — uses standard budget.
func trimToolDescriptionsWithPressure(defs []provider.ToolDefinition, pressure float64) []provider.ToolDefinition {
	budget := pressureBudget(pressure)

	var trimmedCount int
	var bytesSaved int

	for i := range defs {
		d := &defs[i]
		if len(d.Description) <= budget {
			continue
		}
		// Only trim external tool descriptions.
		if !isExtTool(d.Name) {
			continue
		}

		original := d.Description
		trimmed := trimDescriptionTo(original, budget)

		if len(trimmed) < len(original) {
			d.Description = trimmed
			bytesSaved += len(original) - len(trimmed)
			trimmedCount++
		}
	}

	if trimmedCount > 0 {
		debug.Log("toolfilter", "trimmed %d tool descriptions (budget=%d, pressure=%.0f%%), saved ~%d bytes (~%d tokens)",
			trimmedCount, budget, pressure*100, bytesSaved, bytesSaved/4)
	}

	return defs
}

// pressureBudget returns the description byte budget for the given context
// pressure level. Higher pressure = smaller budget.
func pressureBudget(pressure float64) int {
	if pressure <= 0 || pressure < pressureTierModerate {
		return budgetAtModerate
	}
	if pressure < pressureTierHigh {
		return budgetAtModerate
	}
	if pressure < pressureTierCritical {
		return budgetAtHigh
	}
	return budgetAtCritical
}

// trimDescription truncates a description string to fit within the standard
// budget. It tries to cut at the first paragraph boundary (double newline).
// If no paragraph boundary exists within the limit, it cuts at the last
// sentence boundary (period followed by space). If neither exists, it
// hard-cuts at maxDescriptionBytes.
func trimDescription(desc string) string {
	return trimDescriptionTo(desc, maxDescriptionBytes)
}

// trimDescriptionTo truncates a description string to fit within the given
// byte budget. Uses the same paragraph/sentence/hard-cut strategy as
// trimDescription but with a caller-specified budget.
func trimDescriptionTo(desc string, maxBytes int) string {
	if maxBytes <= 0 {
		maxBytes = maxDescriptionBytes
	}
	// Don't trim if we're barely over the limit.
	minLen := maxBytes * 7 / 10 // 70% of budget as minimum trim threshold
	if len(desc) <= minLen {
		return desc
	}

	// Try to cut at the first paragraph boundary (blank line).
	cut := findParagraphCut(desc, maxBytes)
	if cut > 0 {
		return strings.TrimSpace(desc[:cut]) + " …"
	}

	// Try to cut at the last sentence boundary within the limit.
	cut = findSentenceCut(desc, maxBytes)
	if cut > 0 {
		return desc[:cut]
	}

	// Hard cut at maxBytes.
	if maxBytes >= len(desc) {
		return desc
	}
	return desc[:maxBytes] + " …"
}

// findParagraphCut finds the byte position of the first paragraph boundary
// (double newline) within the first maxBytes of the description. Returns 0
// if no boundary is found within the limit.
func findParagraphCut(desc string, maxBytes int) int {
	limit := maxBytes
	if limit > len(desc) {
		limit = len(desc)
	}

	// Look for \n\n (paragraph break) within the limit.
	idx := strings.Index(desc[:limit], "\n\n")
	if idx > 0 {
		return idx
	}

	// Also handle \r\n\r\n (Windows-style).
	idx = strings.Index(desc[:limit], "\r\n\r\n")
	if idx > 0 {
		return idx
	}

	return 0
}

// findSentenceCut finds the byte position after the last sentence-ending
// punctuation within the first maxBytes. Returns 0 if no sentence boundary
// is found, in which case the caller should hard-cut.
func findSentenceCut(desc string, maxBytes int) int {
	limit := maxBytes
	if limit > len(desc) {
		limit = len(desc)
	}

	// Search backwards from the limit for ". " or ".\n".
	for i := limit - 1; i >= 20; i-- {
		if desc[i] == '.' || desc[i] == '!' || desc[i] == '?' {
			// Check that this is followed by whitespace (sentence boundary).
			if i+1 < len(desc) && (desc[i+1] == ' ' || desc[i+1] == '\n' || desc[i+1] == '\r') {
				return i + 1
			}
		}
	}

	return 0
}
