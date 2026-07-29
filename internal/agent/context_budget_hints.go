package agent

import (
	"fmt"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// Proactive context budget efficiency hints.
//
// Research basis: Context engineering (Anthropic 2025) identifies "budget by
// fill %" as a top technique. Chroma's 2025 study shows all frontier models
// degrade past ~50% context fullness, with sharp degradation past 70%.
//
// The existing contextWarningLevel system (in agent.go) only fires at 95%,
// 99%, and 100% — crisis management. By that point, the agent has already
// consumed most of its budget and behavior change is too late.
//
// These hints fire EARLIER (70% and 85% of context window) to proactively
// guide the agent toward context-conserving behavior BEFORE reaching crisis
// levels. UsageRatio() = tokens / contextWindow, so these thresholds map
// directly to fill percentage:
//   - 70% fill: gentle nudge to prefer targeted reads over full files
//   - 85% fill: stronger guidance to batch operations and be concise
//
// This extends the usable context window in practice because the agent
// self-regulates instead of passively filling the window and then triggering
// expensive compaction (which loses nuance from earlier messages).

// contextBudgetHintLevel tracks which proactive hint tiers have been injected.
// Levels are cumulative: once a higher level is reached, lower levels are
// not re-injected.
type contextBudgetHintLevel int

const (
	budgetHintNone     contextBudgetHintLevel = iota
	budgetHintModerate                        // 70% fill: prefer targeted reads
	budgetHintHigh                            // 85% fill: aggressive conservation
)

// maybeInjectBudgetHint checks context fill ratio and injects a proactive
// efficiency hint when crossing the 70% or 85% thresholds for the first time.
// Returns true if a hint was injected (caller should refresh msgs).
func (a *Agent) maybeInjectBudgetHint(currentLevel *contextBudgetHintLevel) bool {
	if a.contextManager.ContextWindow() <= 0 {
		return false
	}

	usage := a.contextManager.UsageRatio()
	var newLevel contextBudgetHintLevel
	var msgText string

	switch {
	case usage >= 0.85 && *currentLevel < budgetHintHigh:
		newLevel = budgetHintHigh
		msgText = "Context at 85%. Conserve: use grep/search_files with specific patterns instead of full read_file; batch tool calls; keep responses concise; avoid re-reading files you already inspected."
	case usage >= 0.70 && *currentLevel < budgetHintModerate:
		newLevel = budgetHintModerate
		msgText = "Context at 70%. Prefer targeted reads (offset/limit) over full files, and batch related inspections to avoid unnecessary context growth."
	}

	if newLevel <= *currentLevel {
		return false
	}

	*currentLevel = newLevel
	debug.Log("agent", "Injecting proactive budget hint level %d at %.0f%% context fill", newLevel, usage*100)
	a.contextManager.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: msgText,
		}},
	})
	return true
}

func (l contextBudgetHintLevel) String() string {
	switch l {
	case budgetHintNone:
		return "none"
	case budgetHintModerate:
		return "moderate(70%)"
	case budgetHintHigh:
		return "high(85%)"
	default:
		return fmt.Sprintf("level(%d)", int(l))
	}
}
