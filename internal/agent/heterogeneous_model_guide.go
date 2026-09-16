package agent

import (
	"sync"
)

// Heterogeneous Model Selection Guide
//
// Research basis: 2025-2026 AI Agent trends (Deloitte, Machine Learning Mastery)
// identify FinOps for AI Agents as a critical frontier concept. The economic
// imperative is to use heterogeneous model architectures:
//   - Expensive frontier models (Claude Opus, GPT-4) for complex reasoning
//   - Mid-tier models for standard tasks
//   - Small language models for high-frequency execution
//
// The Plan-and-Execute pattern reduces costs by 90% compared to using
// frontier models for everything (capable model plans, cheaper models execute).
//
// This module provides heuristic guidance to help agents make cost-effective
// model choices at runtime. It detects when the agent is:
//   1. Doing deep reasoning (justifies frontier model cost)
//   2. Doing routine execution (should use cheaper model)
//
// Design:
//   - Zero LLM cost - pure pattern matching on tool usage
//   - Non-blocking: advisory guidance only
//   - Fires at most once per run
//   - Resets each run

const (
	// hmMinToolActions is the minimum number of tool calls to trigger analysis.
	hmMinToolActions = 5

	// hmExecutionThreshold: if >70% of tools are read/search/grep/edit, it's execution
	hmExecutionThreshold = 0.70

	// hmReasoningThreshold: if >50% are lsp/codex/planning tools, it's reasoning
	hmReasoningThreshold = 0.50

	// hmLookbackWindow: number of recent tool calls to analyze
	hmLookbackWindow = 10

	hmMaxWarns = 1
)

// Tool categories for heterogeneous model guidance
type hmToolCategory int

const (
	hmCategoryRead      hmToolCategory = iota // read_file, search_files, grep
	hmCategoryWrite                           // edit_file, write_file, multi_file_edit
	hmCategorySearch                          // web_search, code_search
	hmCategoryReasoning                       // lsp_* tools requiring semantic understanding
	hmCategoryExecution                       // shell commands, git operations, file ops
	hmCategoryOther                           // everything else
)

// hmClassifyTool returns the category of a tool call.
func hmClassifyTool(toolName string) hmToolCategory {
	switch {
	case toolName == "read_file" || toolName == "multi_file_read":
		return hmCategoryRead
	case toolName == "edit_file" || toolName == "write_file" || toolName == "multi_file_edit" || toolName == "multi_edit_file":
		return hmCategoryWrite
	case toolName == "web_search" || toolName == "code_search" || toolName == "search_files" || toolName == "grep":
		return hmCategorySearch
	case len(toolName) >= 4 && toolName[:4] == "lsp_":
		return hmCategoryReasoning
	case toolName == "run_command" || toolName == "start_command" || toolName == "git_add" ||
		toolName == "git_commit" || toolName == "git_checkout" || toolName == "git_stash" ||
		toolName == "file_ops":
		return hmCategoryExecution
	default:
		return hmCategoryOther
	}
}

// hmToolRecord records a single tool call for analysis.
type hmToolRecord struct {
	tool      string
	category  hmToolCategory
	iteration int
}

// heterogeneousModelState tracks FinOps heterogeneous model guidance.
type heterogeneousModelState struct {
	mu             sync.Mutex
	totalTools     int
	categoryCounts map[hmToolCategory]int
	toolHistory    []hmToolRecord
	warnsIssued    int
	guidance       string
}

// recordToolCall records a tool call and checks if guidance should be emitted.
// Returns guidance text if triggered, empty string otherwise.
func (s *heterogeneousModelState) recordToolCall(toolName string, iteration int) string {
	// Record this tool call
	s.mu.Lock()
	defer s.mu.Unlock()

	cat := hmClassifyTool(toolName)
	s.categoryCounts[cat]++
	s.totalTools++

	// Trim to lookback window. #2427: the trim must leave room for the
	// record we are about to append (`>=`, not `>`) so the steady-state
	// window is exactly hmLookbackWindow entries, and it must happen BEFORE
	// the ratio calculation below reads the slice.
	if len(s.toolHistory) >= hmLookbackWindow {
		s.toolHistory = s.toolHistory[1:]
	}
	s.toolHistory = append(s.toolHistory, hmToolRecord{
		tool:      toolName,
		category:  cat,
		iteration: iteration,
	})

	// Only check after minimum actions
	if s.totalTools < hmMinToolActions {
		return ""
	}

	// Check if we should warn (at most once)
	if s.warnsIssued >= hmMaxWarns {
		return ""
	}

	// Calculate ratios over the SLIDING WINDOW, not lifetime cumulative
	// counts (#2427). The old code summed categoryCounts / totalTools -
	// lifetime aggregates since the last reset - so an exploration-then-
	// execution run (the module's own Plan-and-Execute flagship scenario:
	// early lsp/glob/list calls permanently diluted the denominator) never
	// reached the 70% threshold even when the last 10 calls were pure
	// execution, silently dropping the FinOps downgrade hint. The window
	// slice recorded above is now the ratio's actual data source.
	winLen := len(s.toolHistory)
	if winLen == 0 {
		return ""
	}
	execCount := 0
	reasoningCount := 0
	for _, rec := range s.toolHistory {
		switch rec.category {
		case hmCategoryRead, hmCategoryWrite, hmCategorySearch, hmCategoryExecution:
			execCount++
		case hmCategoryReasoning:
			reasoningCount++
		}
	}
	execRatio := float64(execCount) / float64(winLen)
	reasoningRatio := float64(reasoningCount) / float64(winLen)

	// Determine workload type
	if execRatio >= hmExecutionThreshold {
		s.warnsIssued++
		guidance := hmGenerateGuidance("execution-heavy")
		s.guidance = guidance
		return guidance
	}
	if reasoningRatio >= hmReasoningThreshold {
		// Reasoning-heavy doesn't trigger a warning - it justifies frontier model
		return ""
	}

	return ""
}

// hmGenerateGuidance generates appropriate guidance based on workload type.
func hmGenerateGuidance(workloadType string) string {
	if workloadType == "execution-heavy" {
		return `[FinOps Guidance: Heterogeneous Model Selection]

Your recent actions show an execution-heavy pattern (read/write/search/execution tools dominate).

Cost Optimization Opportunity:
Consider using a cost-effective model tier for routine file operations:
- Frontier models (Claude Opus, GPT-4): Best for complex reasoning, planning, architectural decisions
- Mid-tier models (Claude Sonnet, GPT-4o-mini): Good balance for standard coding tasks
- Small models: Sufficient for repetitive file edits, grep searches, basic text operations

Current pattern appears to be routine execution rather than deep reasoning.
If this is primarily mechanical work, you could reduce token costs by 60-90%
while maintaining quality.

This is guidance only - proceed with the current model if this task requires
frontier-level reasoning capability.`
	}
	return ""
}

// newHeterogeneousModelState creates a new heterogeneous model guide instance.
func newHeterogeneousModelState() *heterogeneousModelState {
	return &heterogeneousModelState{
		categoryCounts: make(map[hmToolCategory]int),
	}
}

// reset clears accumulated state for a new run.
func (s *heterogeneousModelState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.totalTools = 0
	s.categoryCounts = make(map[hmToolCategory]int)
	s.toolHistory = nil
	s.warnsIssued = 0
	s.guidance = ""
}

// GetHeterogeneousModelGuidance returns any pending heterogeneous model guidance.
// #2427: take the state mutex - recordToolCall writes s.guidance under it;
// an unlocked read here raced the string write (multi-word torn read).
func (a *Agent) GetHeterogeneousModelGuidance() string {
	if a.heterogeneousModel == nil {
		return ""
	}
	a.heterogeneousModel.mu.Lock()
	defer a.heterogeneousModel.mu.Unlock()
	return a.heterogeneousModel.guidance
}

// ClearHeterogeneousModelGuidance clears the pending guidance after it's been consumed.
// #2427: same mutex as the writer.
func (a *Agent) ClearHeterogeneousModelGuidance() {
	if a.heterogeneousModel != nil {
		a.heterogeneousModel.mu.Lock()
		a.heterogeneousModel.guidance = ""
		a.heterogeneousModel.mu.Unlock()
	}
}
