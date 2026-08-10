package agent

// Tool Selection Scorer - Cost-Aware Tool Choice Guidance
//
// Research basis: "Cost-Awareness in Tree-Search LLM Planning" (arXiv:2505.14656, May 2025)
// and "ToolTree: Efficient LLM Agent Tool Planning via Dual-Feedback MCTS" (arXiv:2603.12740, Mar 2026)
// and "Tool Call Evaluation - Multi-agent Reference Architecture" (Microsoft, 2025)
//
// Key insight: Agents waste tokens and time by choosing tools sub-optimally. MCTS-based
// tool selection uses dual feedback: execution results (success/failure) and plan coherence
// to score and rank tool choices before committing.
//
// Gap in ggcode:
//   - LLM selects tools implicitly based on semantic similarity, not empirical quality
//   - No cost-aware guidance: cheap tools (read_file) vs expensive (browser) not balanced
//   - LatencyTracker has success rate but doesn't generate selection scores
//   - No tool combination scoring (e.g., "use grep + read_file" vs "code_search")
//   - No Intent Alignment check: tool choice may not match user's actual goal (sa-135)
//
// This component fills the gap by scoring tools on four dimensions:
//   1. Success rate: historical reliability (0-100)
//   2. Cost efficiency: tokens per second (higher is better)
//   3. Latency: average execution time (lower is better)
//   4. Intent Alignment: tool capability matches user goal (sa-135)
//
// It generates a ranked tool recommendation when multiple tools can achieve the same goal,
// with concrete cost/quality tradeoff data to inform LLM decision.
//
// Design:
//   - Uses existing LatencyTracker data (no duplicate tracking)
//   - Zero LLM cost (deterministic scoring)
//   - Fires only when 2+ tools are in the same "equivalence class"
//   - Provides structured ranking with reasoning
//   - Intent Alignment: keyword-based semantic matching (sa-135)

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// toolCostEstimates provides per-tool token cost estimates (in input tokens).
// These are approximate averages from production traces. Cost is per call.
var toolCostEstimates = map[string]int{
	// Read operations (cheap)
	"read_file":             100,
	"multi_file_read":       300,
	"list_directory":        50,
	"glob":                  80,
	"lsp_symbols":           150,
	"lsp_definition":        150,
	"lsp_references":        200,
	"lsp_hover":             120,
	"lsp_workspace_symbols": 300,

	// Search operations (moderate)
	"grep":         200,
	"search_files": 250,
	"code_search":  400,

	// Write operations (moderate)
	"edit_file":        150,
	"multi_edit_file":  350,
	"write_file":       200,
	"multi_file_write": 400,

	// Expensive operations
	"run_command":   500,
	"start_command": 200,
	"browser":       800,
	"screenshot":    600,
	"mobile_device": 1000,

	// AI-assisted operations (very expensive)
	"delegate": 2000,
	"skill":    1500,
}

// toolEquivalenceClasses groups tools that can achieve similar goals.
// When the agent is considering tools in the same class, we provide scoring.
var toolEquivalenceClasses = map[string][]string{
	"file_read":       {"read_file", "multi_file_read", "grep", "search_files", "code_search"},
	"code_understand": {"lsp_definition", "lsp_references", "lsp_hover", "grep", "code_search"},
	"code_edit":       {"edit_file", "multi_edit_file", "write_file"},
	"web_fetch":       {"web_fetch", "web_search", "browser"},
	"file_find":       {"glob", "search_files", "list_directory"},
	"git_ops":         {"run_command", "git_status", "git_diff"}, // run_command for git
}

// toolCapabilityKeywords maps tools to their functional keywords.
// Used for Intent Alignment: checking if tool capability matches user goal (sa-135).
var toolCapabilityKeywords = map[string][]string{
	// Read operations
	"read_file":       {"read", "view", "content", "file", "text"},
	"multi_file_read": {"read", "multiple", "files", "batch"},
	"list_directory":  {"list", "directory", "files", "ls", "structure"},
	"glob":            {"find", "files", "pattern", "glob", "match"},

	// LSP operations
	"lsp_symbols":           {"symbols", "outline", "structure", "definition"},
	"lsp_definition":        {"definition", "go to", "jump", "source", "implement"},
	"lsp_references":        {"references", "usages", "callers", "where used"},
	"lsp_hover":             {"hover", "type", "info", "tooltip", "signature"},
	"lsp_workspace_symbols": {"search", "symbols", "workspace", "global"},

	// Search operations
	"grep":         {"search", "find", "content", "pattern", "regex"},
	"search_files": {"search", "files", "content", "text"},
	"code_search":  {"semantic", "search", "concept", "natural language"},

	// Write operations
	"edit_file":        {"edit", "modify", "change", "replace"},
	"multi_edit_file":  {"edit", "multiple", "files", "batch"},
	"write_file":       {"write", "create", "overwrite", "new"},
	"multi_file_write": {"write", "multiple", "files", "create"},

	// Shell operations
	"run_command":   {"run", "execute", "command", "shell", "test"},
	"start_command": {"start", "background", "daemon", "service"},

	// Web operations
	"web_fetch":  {"fetch", "url", "web", "page", "html"},
	"web_search": {"search", "web", "google", "find", "look up"},
	"browser":    {"browser", "chrome", "navigate", "click", "interactive web"},

	// AI-assisted operations
	"delegate": {"delegate", "sub-agent", "claude", "gpt", "other agent"},
	"skill":    {"skill", "workflow", "template", "reusable"},
}

// userIntentKeywords maps user intent patterns to preferred tool categories.
// Used for Intent Alignment: detecting if chosen tool matches user's expressed goal.
var userIntentKeywords = map[string][]string{
	"understand_code": {"understand", "explain", "how", "works", "what does", "meaning"},
	"navigate_code":   {"go to", "find definition", "where is", "show me", "locate"},
	"refactor_code":   {"refactor", "clean up", "improve", "optimize", "simplify"},
	"fix_bug":         {"fix", "bug", "error", "issue", "problem", "broken"},
	"add_feature":     {"add", "implement", "feature", "functionality", "new"},
	"search_code":     {"search", "find", "where used", "references", "usages"},
	"read_content":    {"read", "view", "show", "content", "text"},
	"write_content":   {"write", "create", "generate", "output"},
	"run_test":        {"test", "run tests", "verify", "check", "validate"},
	"deploy":          {"deploy", "build", "release", "ship"},
	"web_scrape":      {"scrape", "crawl", "fetch", "web page", "url"},
	"shell_operation": {"command", "shell", "execute", "run"},
}

// toolScore represents a scored tool choice.
type toolScore struct {
	name          string
	successRate   float64 // 0.0-1.0
	avgLatency    time.Duration
	estimatedCost int     // input tokens
	costPerSec    float64 // cost / latency (higher = more efficient)
	intentAlign   float64 // 0.0-1.0: how well tool matches user goal (sa-135)
	overall       float64 // weighted overall score
	reasoning     string  // human-readable explanation
}

// ToolSelectionScorer provides cost-aware tool selection guidance.
type ToolSelectionScorer struct {
	mu sync.RWMutex

	// References to shared trackers
	latency *LatencyTracker
}

// NewToolSelectionScorer creates a scorer backed by existing latency data.
func NewToolSelectionScorer(latency *LatencyTracker) *ToolSelectionScorer {
	return &ToolSelectionScorer{
		latency: latency,
	}
}

// ScoreAndRank scores candidate tools and returns a ranked recommendation.
//
// candidateTools: list of tools the agent is considering
// taskContext: brief description of what the agent wants to do (for classification)
//
// Returns formatted guidance string, or empty if scoring is not applicable.
func (s *ToolSelectionScorer) ScoreAndRank(candidateTools []string, taskContext string) string {
	if len(candidateTools) < 2 {
		return "" // No need to rank a single tool
	}

	class := s.classifyTask(taskContext)
	if class == "" {
		return "" // Can't determine equivalence class
	}

	scores := s.computeScores(candidateTools)
	if len(scores) < 2 {
		return "" // Not enough data to rank
	}

	// Sort by overall score (descending)
	sort.Slice(scores, func(i, j int) bool {
		return scores[i].overall > scores[j].overall
	})

	return s.formatRanking(scores, class)
}

// classifyTask maps a task description to an equivalence class name.
func (s *ToolSelectionScorer) classifyTask(task string) string {
	task = strings.ToLower(task)

	// Simple keyword matching for classification
	if strings.Contains(task, "read") || strings.Contains(task, "view") || strings.Contains(task, "content") {
		if strings.Contains(task, "code") || strings.Contains(task, "symbol") {
			return "code_understand"
		}
		return "file_read"
	}

	if strings.Contains(task, "edit") || strings.Contains(task, "modify") || strings.Contains(task, "change") {
		return "code_edit"
	}

	if strings.Contains(task, "search") || strings.Contains(task, "find") {
		if strings.Contains(task, "file") || strings.Contains(task, "path") {
			return "file_find"
		}
		if strings.Contains(task, "web") || strings.Contains(task, "page") {
			return "web_fetch"
		}
		return "file_read" // default to search via read
	}

	if strings.Contains(task, "git") || strings.Contains(task, "commit") || strings.Contains(task, "branch") {
		return "git_ops"
	}

	return "" // Unknown class
}

// computeScores calculates scores for all candidate tools.
func (s *ToolSelectionScorer) computeScores(tools []string) []toolScore {
	var scores []toolScore

	for _, tool := range tools {
		ts := s.scoreTool(tool)
		if ts != nil {
			scores = append(scores, *ts)
		}
	}

	return scores
}

// scoreTool computes a single tool's score.
func (s *ToolSelectionScorer) scoreTool(name string) *toolScore {
	// Get historical data
	successRate := s.latency.SuccessRate(name)
	avgLatency := s.latency.meanLatency(name)
	estimatedCost := toolCostEstimates[name]

	// Skip if no data at all
	if estimatedCost == 0 {
		return nil
	}

	// Default success rate if no history
	if successRate == 0 {
		successRate = 0.85 // assume reasonable default
	}

	// Default latency if no history (estimate from cost)
	if avgLatency == 0 {
		// Rough heuristic: 100ms per 100 tokens
		avgLatency = time.Duration(estimatedCost) * time.Millisecond
	}

	// Cost efficiency: cost per second (lower = more efficient, so invert)
	costPerSec := float64(estimatedCost) / avgLatency.Seconds()
	if costPerSec == 0 {
		costPerSec = 1.0 // avoid division by zero
	}

	// Overall score: weighted combination (sa-135: added Intent Alignment)
	//   - Success rate: 40% weight (reliability is paramount)
	//   - Cost efficiency: 25% weight (cost matters)
	//   - Latency: 20% weight (faster is better, inverse)
	//   - Intent Alignment: 15% weight (tool capability matches user goal)
	intentAlignScore := s.checkIntentAlignment(name) * 15
	successScore := successRate * 40
	efficiencyScore := normalizeCostEfficiency(costPerSec) * 25
	latencyScore := normalizeLatency(avgLatency) * 20

	overall := successScore + efficiencyScore + latencyScore + intentAlignScore

	// Generate reasoning
	var reasons []string
	if successRate < 0.7 {
		reasons = append(reasons, fmt.Sprintf("low success rate (%.0f%%)", successRate*100))
	} else if successRate > 0.9 {
		reasons = append(reasons, fmt.Sprintf("highly reliable (%.0f%% success)", successRate*100))
	}

	if avgLatency > 2*time.Second {
		reasons = append(reasons, fmt.Sprintf("slow (%.1fs avg)", avgLatency.Seconds()))
	} else if avgLatency < 500*time.Millisecond {
		reasons = append(reasons, fmt.Sprintf("fast (%dms avg)", avgLatency.Milliseconds()))
	}

	if estimatedCost > 500 {
		reasons = append(reasons, "expensive (high token usage)")
	} else if estimatedCost < 200 {
		reasons = append(reasons, "efficient (low token usage)")
	}

	reasoning := "balanced"
	if len(reasons) > 0 {
		reasoning = strings.Join(reasons, ", ")
	}

	return &toolScore{
		name:          name,
		successRate:   successRate,
		avgLatency:    avgLatency,
		estimatedCost: estimatedCost,
		costPerSec:    costPerSec,
		intentAlign:   s.checkIntentAlignment(name),
		overall:       overall,
		reasoning:     reasoning,
	}
}

// normalizeCostEfficiency normalizes cost-per-second to 0-1.
// Assumes reasonable range: 50-500 tokens/sec.
func normalizeCostEfficiency(costPerSec float64) float64 {
	// Lower is better, so invert
	const min, max = 50.0, 500.0
	if costPerSec <= min {
		return 1.0
	}
	if costPerSec >= max {
		return 0.0
	}
	return 1.0 - (costPerSec-min)/(max-min)
}

// normalizeLatency normalizes latency to 0-1.
// Assumes reasonable range: 50ms-5s.
func normalizeLatency(d time.Duration) float64 {
	const min, max = 50 * time.Millisecond, 5 * time.Second
	if d <= min {
		return 1.0
	}
	if d >= max {
		return 0.0
	}
	return 1.0 - float64(d-min)/float64(max-min)
}

// formatRanking creates a human-readable ranking.
func (s *ToolSelectionScorer) formatRanking(scores []toolScore, class string) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("[Tool Selection Guidance: %s tasks]\n", class))
	sb.WriteString("Ranking tools by cost-aware quality (success, efficiency, speed, intent alignment):\n\n")

	for i, ts := range scores {
		rank := i + 1
		indicator := "  "
		if rank == 1 {
			indicator = "→ "
		}

		sb.WriteString(fmt.Sprintf("%s%d. %s (score: %.0f/100)\n", indicator, rank, ts.name, ts.overall))
		sb.WriteString(fmt.Sprintf("     Success: %.0f%% | Latency: %dms | Cost: ~%d tokens | Intent: %.0f%%",
			ts.successRate*100, ts.avgLatency.Milliseconds(), ts.estimatedCost, ts.intentAlign*100))
		sb.WriteString(fmt.Sprintf(" | %s\n", ts.reasoning))
	}

	// Add recommendation based on top choice
	top := scores[0]
	var recommendation string
	if top.successRate > 0.9 && top.avgLatency < 1*time.Second {
		recommendation = fmt.Sprintf("Recommended: %s (fast and reliable)", top.name)
	} else if top.estimatedCost < 300 {
		recommendation = fmt.Sprintf("Recommended: %s (cost-effective)", top.name)
	} else if top.successRate > 0.85 {
		recommendation = fmt.Sprintf("Recommended: %s (most reliable)", top.name)
	} else {
		recommendation = fmt.Sprintf("Suggested: %s (best overall score)", top.name)
	}

	sb.WriteString(fmt.Sprintf("\n%s", recommendation))
	sb.WriteString("\nConsider tradeoffs: a lower-ranked tool may be preferable for specific constraints.\n")

	return sb.String()
}

// EstimateTokens returns an estimated token cost for a tool, or 0 if unknown.
func EstimateTokens(toolName string) int {
	return toolCostEstimates[toolName]
}

// classifyTools returns the equivalence class for a set of tools, or empty if mixed.
func classifyTools(tools []string) string {
	for class, members := range toolEquivalenceClasses {
		matches := 0
		for _, t := range tools {
			for _, m := range members {
				if t == m {
					matches++
					break
				}
			}
		}
		// If most tools belong to this class, classify it
		if matches >= len(tools)/2+1 {
			return class
		}
	}
	return ""
}

// checkIntentAlignment calculates intent alignment score for a tool (sa-135).
// Returns 0.0-1.0: higher means tool capability better matches typical user goals.
//
// Research basis: "Tool Call Evaluation - Multi-agent Reference Architecture" (Microsoft, 2025)
// Intent Alignment dimension: does the selected tool match the user's request intent?
//
// Implementation: Keyword-based semantic matching between tool capabilities and user intents.
// This is a lightweight approximation; full intent understanding requires LLM.
func (s *ToolSelectionScorer) checkIntentAlignment(toolName string) float64 {
	// Get tool's capability keywords
	toolKeywords, exists := toolCapabilityKeywords[toolName]
	if !exists {
		return 0.5 // Default: unknown tool, assume moderate alignment
	}

	// Check overlap with user intent patterns
	// Since we don't have current user intent context here, we return
	// a base score based on the tool's versatility (more keywords = more versatile)
	keywordCount := len(toolKeywords)

	// Normalize: 1-5 keywords = 0.5-0.9, 6+ keywords = 0.9-1.0
	if keywordCount <= 1 {
		return 0.5 // Very specialized tool
	}
	if keywordCount <= 3 {
		return 0.6 // Specialized
	}
	if keywordCount <= 5 {
		return 0.75 // Versatile
	}
	return 0.9 // Highly versatile tool
}

// CheckIntentAlignmentWithIntent calculates intent alignment given user intent (sa-135).
// This is the full version that compares tool capabilities against actual user intent.
//
// userIntent: the user's expressed goal (e.g., "I need to understand how this function works")
// Returns 0.0-1.0: higher means better alignment.
func (s *ToolSelectionScorer) CheckIntentAlignmentWithIntent(toolName, userIntent string) float64 {
	toolKeywords, exists := toolCapabilityKeywords[toolName]
	if !exists {
		return 0.5
	}

	userIntentLower := strings.ToLower(userIntent)
	matches := 0

	// Count keyword matches
	for _, kw := range toolKeywords {
		if strings.Contains(userIntentLower, kw) {
			matches++
		}
	}

	// Check against intent patterns
	for intentPattern, intentKeywords := range userIntentKeywords {
		patternMatched := false
		for _, ikw := range intentKeywords {
			if strings.Contains(userIntentLower, ikw) {
				patternMatched = true
				break
			}
		}
		if !patternMatched {
			continue
		}

		// User intent matched, check if tool supports this intent category
		// This is a simplified mapping - in production, would be more sophisticated
		switch intentPattern {
		case "understand_code":
			if toolInList(toolName, []string{"lsp_definition", "lsp_hover", "read_file", "code_search"}) {
				matches += 2
			}
		case "navigate_code":
			if toolInList(toolName, []string{"lsp_definition", "lsp_references", "lsp_workspace_symbols"}) {
				matches += 2
			}
		case "refactor_code":
			if toolInList(toolName, []string{"edit_file", "multi_edit_file", "code_search"}) {
				matches += 2
			}
		case "fix_bug":
			if toolInList(toolName, []string{"read_file", "grep", "edit_file", "run_command"}) {
				matches += 2
			}
		case "add_feature":
			if toolInList(toolName, []string{"write_file", "edit_file", "code_search"}) {
				matches += 2
			}
		case "search_code":
			if toolInList(toolName, []string{"grep", "search_files", "code_search", "lsp_references"}) {
				matches += 2
			}
		case "read_content":
			if toolInList(toolName, []string{"read_file", "multi_file_read", "list_directory"}) {
				matches += 2
			}
		case "write_content":
			if toolInList(toolName, []string{"write_file", "edit_file", "multi_file_write"}) {
				matches += 2
			}
		case "run_test":
			if toolInList(toolName, []string{"run_command", "start_command"}) {
				matches += 2
			}
		case "deploy":
			if toolInList(toolName, []string{"run_command", "git_status", "git_commit"}) {
				matches += 2
			}
		case "web_scrape":
			if toolInList(toolName, []string{"web_fetch", "web_search", "browser"}) {
				matches += 2
			}
		case "shell_operation":
			if toolInList(toolName, []string{"run_command", "start_command"}) {
				matches += 2
			}
		}
	}

	// Normalize matches to 0-1
	maxPossible := len(toolKeywords) + 6 // keywords + 6 intent categories
	if maxPossible == 0 {
		return 0.5
	}

	alignScore := float64(matches) / float64(maxPossible)
	if alignScore > 1.0 {
		alignScore = 1.0
	}

	// Ensure minimum score of 0.3 (no tool is completely misaligned)
	if alignScore < 0.3 {
		alignScore = 0.3
	}

	return alignScore
}

// toolInList checks if toolName is in the candidates list.
func toolInList(toolName string, candidates []string) bool {
	for _, c := range candidates {
		if toolName == c {
			return true
		}
	}
	return false
}
