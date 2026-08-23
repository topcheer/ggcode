package tool

import (
	"strings"
	"unicode"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// RelevanceFilter dynamically prunes low-relevance tools from the tool
// definitions sent to the LLM on each API call. This addresses the "tool
// overload" problem documented in the RAG-MCP paper: when tool counts exceed
// ~15-20, agent accuracy degrades measurably due to attention dilution and
// "lost in the middle" effects.
//
// Design principles:
//  1. Built-in tools are always kept — they are core to the agent's function
//     and well-understood by the LLM.
//  2. MCP/plugin tools (names containing "__") are scored against the current
//     conversation context. Only those with a relevance score above the
//     threshold are included.
//  3. The filter only activates when total tool count exceeds activationThreshold.
//     Below that, all tools are sent — no overhead.
//  4. Zero-cost: scoring uses lightweight keyword matching, no LLM calls.
const (
	// minToolsToActivate is the minimum total tool count before filtering
	// kicks in. Below this, sending all tools is fine.
	minToolsToActivate = 30

	// mcpScoreThreshold is the minimum relevance score (0.0–1.0) for an MCP
	// tool to be included when filtering is active. 0.12 means a tool with a
	// ~20-token description needs at least 2-3 overlapping tokens (or one
	// tool-name token match, which carries a +0.15 boost) to survive — a
	// single generic description word no longer keeps a tool alive.
	mcpScoreThreshold = 0.12

	// maxMCPTools is the maximum number of MCP tools to include per server
	// when filtering is active. This prevents a single server from dominating.
	maxMCPToolsPerServer = 15
)

// RelevanceFilter scores and filters tool definitions based on relevance
// to the current conversation context.
type RelevanceFilter struct {
	// activated tracks whether filtering was active in the last call.
	activated bool
	// prunedCount tracks how many tools were pruned for diagnostics.
	prunedCount int
	// prevPruned tracks the set of pruned tool names from the previous call.
	// Used to avoid log spam — only log when the pruned set changes.
	prevPrunedKey string
}

// NewRelevanceFilter creates a new filter instance.
func NewRelevanceFilter() *RelevanceFilter {
	return &RelevanceFilter{}
}

// Filter returns a potentially reduced set of tool definitions based on
// relevance to the given context string (typically the last user message +
// recent conversation).
//
// If the total tool count is below minToolsToActivate, all tools are returned
// unchanged. Otherwise, built-in tools are always kept, and MCP/plugin tools
// are scored — only those with sufficient relevance are included.
func (f *RelevanceFilter) Filter(defs []provider.ToolDefinition, context string) []provider.ToolDefinition {
	return f.FilterWithPressure(defs, context, 0)
}

// FilterWithPressure is like Filter but also applies context-pressure-aware
// description budgeting. When context utilization is high, tool descriptions
// are trimmed more aggressively to free context tokens for conversation.
//
// pressure is the context window utilization ratio (0.0–1.0). Pass 0 or
// negative to disable pressure-aware behavior (uses standard budget).
func (f *RelevanceFilter) FilterWithPressure(defs []provider.ToolDefinition, context string, pressure float64) []provider.ToolDefinition {
	if len(defs) <= minToolsToActivate {
		f.activated = false
		f.prunedCount = 0
		// Even when relevance filtering is not active, still apply description
		// budgeting — verbose MCP descriptions waste context on every turn
		// regardless of total tool count.
		return trimToolDescriptionsWithPressure(defs, pressure)
	}

	f.activated = true

	// Normalize context once.
	ctxTokens := relevanceTokenize(context)
	ctxSet := make(map[string]bool, len(ctxTokens))
	for _, t := range ctxTokens {
		ctxSet[t] = true
	}

	// Also extract the last few user messages' text for broader context.
	// The caller should pass a representative context string.

	var (
		result      = make([]provider.ToolDefinition, 0, len(defs))
		mcpByServer = make(map[string][]scoredTool)
		prunedNames []string
	)

	for _, d := range defs {
		if !isExtTool(d.Name) {
			// Built-in tool — always keep.
			result = append(result, d)
			continue
		}

		// MCP/plugin tool — score it.
		score := scoreTool(d, ctxSet)
		srvName := serverFromName(d.Name)

		st := scoredTool{def: d, score: score}
		if score >= mcpScoreThreshold {
			mcpByServer[srvName] = append(mcpByServer[srvName], st)
		} else {
			prunedNames = append(prunedNames, d.Name)
		}
	}

	// Add qualifying MCP tools, capped per server.
	for _, tools := range mcpByServer {
		// Sort by score descending (simple insertion sort — small N).
		for i := 1; i < len(tools); i++ {
			for j := i; j > 0 && tools[j].score > tools[j-1].score; j-- {
				tools[j], tools[j-1] = tools[j-1], tools[j]
			}
		}
		limit := len(tools)
		if limit > maxMCPToolsPerServer {
			limit = maxMCPToolsPerServer
		}
		for k := 0; k < limit; k++ {
			result = append(result, tools[k].def)
		}
		if len(tools) > limit {
			for k := limit; k < len(tools); k++ {
				prunedNames = append(prunedNames, tools[k].def.Name)
			}
		}
	}

	f.prunedCount = len(defs) - len(result)

	// Log only when the pruned set changes to avoid spam.
	prunedKey := strings.Join(sortedStrings(prunedNames), ",")
	if prunedKey != f.prevPrunedKey && f.prunedCount > 0 {
		f.prevPrunedKey = prunedKey
		debug.Log("toolfilter", "pruned %d/%d tools (kept %d): pruned=[%s]",
			f.prunedCount, len(defs), len(result), truncateForLog(prunedKey, 300))
	}

	// Apply description token budget with context pressure awareness: trim
	// verbose MCP/plugin tool descriptions to reduce per-turn schema overhead.
	// Under high context pressure, descriptions are trimmed more aggressively.
	result = trimToolDescriptionsWithPressure(result, pressure)

	return result
}

// scoredTool pairs a tool definition with its relevance score.
type scoredTool struct {
	def   provider.ToolDefinition
	score float64
}

// isExtTool returns true if the tool name indicates an external (MCP or plugin)
// tool, which follows the naming convention "prefix__server__tool".
func isExtTool(name string) bool {
	return strings.Contains(name, "__") || strings.HasPrefix(name, "mcp_")
}

// serverFromName extracts the server name from an MCP tool name.
// "mcp__railway__list_projects" -> "railway"
func serverFromName(name string) string {
	parts := strings.Split(name, "__")
	if len(parts) >= 2 {
		return parts[len(parts)-2]
	}
	return "_unknown"
}

// scoreTool computes a relevance score for a tool against the context token set.
// The score is based on keyword overlap between the tool's name, description,
// and the context tokens. Returns a value in [0.0, 1.0].
func scoreTool(d provider.ToolDefinition, ctxSet map[string]bool) float64 {
	if len(ctxSet) == 0 {
		// No context — keep everything with a minimal score.
		return mcpScoreThreshold
	}

	// Tokenize tool name and description.
	nameTokens := relevanceTokenize(d.Name)
	descTokens := relevanceTokenize(d.Description)

	// Build tool token set.
	toolSet := make(map[string]bool, len(nameTokens)+len(descTokens))
	for _, t := range nameTokens {
		// Weight name tokens higher by adding them unconditionally.
		toolSet[t] = true
	}
	for _, t := range descTokens {
		toolSet[t] = true
	}

	// Count overlapping tokens.
	matches := 0
	nameMatched := false
	for t := range toolSet {
		if ctxSet[t] {
			matches++
			// Check if this token is part of the tool name (stronger signal).
			for _, nt := range nameTokens {
				if nt == t {
					nameMatched = true
					break
				}
			}
		}
	}

	if matches == 0 {
		return 0.0
	}

	// Score: ratio of matched context tokens, boosted by name matches.
	score := float64(matches) / float64(len(toolSet))
	if nameMatched {
		score += 0.15 // Name keyword match is a strong signal.
	}
	if score > 1.0 {
		score = 1.0
	}

	return score
}

// tokenize splits text into lowercase word tokens, filtering out very short
// tokens and common stop words.
func relevanceTokenize(text string) []string {
	text = strings.ToLower(text)
	var tokens []string
	var sb strings.Builder

	flush := func() {
		s := sb.String()
		sb.Reset()
		if len(s) >= 2 && !isStopWord(s) {
			tokens = append(tokens, s)
		}
	}

	for _, r := range text {
		// NOTE: '_' and '-' are treated as separators, NOT token characters.
		// Treating them as token chars collapsed names like
		// "mcp__github__search_commits" into one opaque token, which made the
		// nameMatched boost in scoreTool dead code for every MCP tool and left
		// scoring entirely at the mercy of generic description words — the
		// primary cause of irrelevant MCP tools surviving the filter.
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			sb.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()

	return tokens
}

// isStopWord returns true for common English words that carry little
// semantic signal for tool matching.
func isStopWord(s string) bool {
	switch s {
	case "the", "a", "an", "is", "are", "was", "were", "be", "been", "being",
		"to", "of", "in", "on", "at", "by", "for", "with", "from", "as",
		"and", "or", "but", "not", "no", "if", "then", "else", "when",
		"this", "that", "these", "those", "it", "its", "they", "them",
		"you", "your", "my", "me", "we", "our", "us", "i",
		"can", "will", "would", "should", "could", "may", "might", "must",
		"do", "does", "did", "have", "has", "had", "get", "got",
		"about", "into", "out", "up", "down", "all", "any", "some", "over",
		"use", "using", "used", "tool", "action", "perform", "via", "parameter":
		return true
	}
	// "mcp" appears in every MCP tool name; keeping it would make any user
	// message that merely mentions "MCP" match every MCP tool's name tokens.
	// The rest are generic API verbs/nouns that appear in most tool
	// descriptions and carry no discrimination signal.
	switch s {
	case "mcp",
		"list", "get", "create", "update", "delete", "remove",
		"fetch", "query", "data", "info", "name", "id", "file", "files":
		return true
	}
	return false
}

// sortedStrings returns a sorted copy of the input slice.
func sortedStrings(ss []string) []string {
	out := make([]string, len(ss))
	copy(out, ss)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// truncateForLog truncates a string for logging purposes.
func truncateForLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// ExtractContextFromMessages builds a context string from the most recent
// conversation messages for relevance scoring. It uses the last few user
// messages and recent assistant text.
func ExtractContextFromMessages(msgs []provider.Message, window int) string {
	if window <= 0 {
		window = 6
	}

	start := len(msgs) - window
	if start < 0 {
		start = 0
	}

	var parts []string
	for i := start; i < len(msgs); i++ {
		m := msgs[i]
		// Prioritize user messages.
		if m.Role == "user" {
			for _, b := range m.Content {
				if b.Type == "text" && len(b.Text) > 0 {
					parts = append(parts, b.Text)
				}
			}
		}
	}

	// If no user messages in window, fall back to assistant messages.
	if len(parts) == 0 {
		for i := start; i < len(msgs); i++ {
			m := msgs[i]
			for _, b := range m.Content {
				if b.Type == "text" && len(b.Text) > 0 {
					parts = append(parts, b.Text)
				}
			}
		}
	}

	combined := strings.Join(parts, " ")
	// Cap context length to avoid expensive tokenization.
	if len(combined) > 2000 {
		combined = combined[:2000]
	}
	return combined
}
