package agent

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/tool"
)

// BudgetTracker provides runtime budget awareness for tool-augmented agents.
// Based on "Budget-Aware Tool-Use Enables Effective Agent Scaling" (arXiv:2511.17006),
// it tracks cumulative cost consumption and provides adaptive planning guidance.
//
// Key concepts:
// - Budget awareness: Agent knows its remaining budget and spends more wisely
// - Adaptive strategy: Decides to "dig deeper" on promising leads or "pivot" to new paths
// - Unified cost metric: Accounts for both token and tool consumption
type BudgetTracker struct {
	mu sync.Mutex

	// TotalBudgetUSD is the maximum allowed cost in USD for this session.
	// 0 means no budget limit (unconstrained mode).
	TotalBudgetUSD float64

	// SpentUSD tracks cumulative cost consumed by tool calls.
	SpentUSD float64

	// ToolCallCount records the total number of tool calls made.
	ToolCallCount int

	// StartTime records when the tracker was initialized.
	StartTime time.Time

	// LastReset records the last time the budget was reset.
	LastReset time.Time
}

// NewBudgetTracker creates a new budget tracker with the specified limit.
// A budget of 0 means unconstrained (no budget awareness).
func NewBudgetTracker(budgetUSD float64) *BudgetTracker {
	return &BudgetTracker{
		TotalBudgetUSD: budgetUSD,
		StartTime:      time.Now(),
		LastReset:      time.Now(),
	}
}

// RecordToolCall records the cost of a tool call and returns budget status.
// It retrieves cost estimate from ToolMeta if available, otherwise uses a default.
func (bt *BudgetTracker) RecordToolCall(_ string, t tool.Tool) string {
	if bt == nil {
		return ""
	}

	bt.mu.Lock()
	defer bt.mu.Unlock()

	cost := bt.estimateToolCost(t)
	bt.SpentUSD += cost
	bt.ToolCallCount++

	// Only provide guidance if budget is constrained.
	if bt.TotalBudgetUSD <= 0 {
		return ""
	}

	return bt.formatBudgetStatus()
}

// estimateToolCost returns the estimated cost for a tool call.
// Uses ToolMeta if available, otherwise estimates based on tool type.
func (bt *BudgetTracker) estimateToolCost(t tool.Tool) float64 {
	if mp, ok := t.(tool.MetaProvider); ok {
		meta := mp.ToolMeta()
		if meta.CostEstimate > 0 {
			return meta.CostEstimate
		}
	}

	// Fallback: heuristic cost estimation based on tool category.
	name := strings.ToLower(t.Name())
	switch {
	case strings.Contains(name, "web_search") || strings.Contains(name, "web_fetch"):
		return 0.001 // ~1 millisecond of compute or API cost
	case strings.Contains(name, "browser"):
		return 0.005 // Browser automation is more expensive
	case strings.Contains(name, "code_search") || strings.Contains(name, "semantic_search"):
		return 0.002 // Vector search operations
	case strings.Contains(name, "lsp"):
		return 0.0001 // Local LSP operations are cheap
	case strings.Contains(name, "run_command") || strings.Contains(name, "start_command"):
		return 0.0 // Execution cost is platform-dependent, not tracked here
	default:
		return 0.0005 // Default cheap local operation
	}
}

// formatBudgetStatus returns a user-friendly budget status string with adaptive guidance.
func (bt *BudgetTracker) formatBudgetStatus() string {
	remaining := bt.TotalBudgetUSD - bt.SpentUSD
	remainingPct := (remaining / bt.TotalBudgetUSD) * 100

	var guidance string
	switch {
	case remainingPct > 70:
		guidance = "Budget充足：可以深入探索多个路径或验证多个假设。"
	case remainingPct > 40:
		guidance = "Budget充足：可以继续当前策略，但注意平衡探索深度。"
	case remainingPct > 20:
		guidance = "Budget警告：考虑优先处理高价值任务，避免在单个路径上过度投入。"
	case remainingPct > 5:
		guidance = "Budget紧张：建议转向更直接的解决方案，减少探索性工具调用。"
	default:
		guidance = "Budget即将耗尽：仅执行最关键的操作，优先完成已开始的任务。"
	}

	return fmt.Sprintf(
		"[Budget Tracker] 已消耗: $%.4f / $%.2f (%.1f%% 剩余) - %s",
		bt.SpentUSD,
		bt.TotalBudgetUSD,
		remainingPct,
		guidance,
	)
}

// RemainingBudgetUSD returns the remaining budget in USD.
func (bt *BudgetTracker) RemainingBudgetUSD() float64 {
	if bt == nil {
		return 0
	}
	bt.mu.Lock()
	defer bt.mu.Unlock()
	return bt.TotalBudgetUSD - bt.SpentUSD
}

// RemainingBudgetPct returns the remaining budget as a percentage (0-100).
func (bt *BudgetTracker) RemainingBudgetPct() float64 {
	if bt == nil || bt.TotalBudgetUSD <= 0 {
		return 0
	}
	bt.mu.Lock()
	defer bt.mu.Unlock()
	return ((bt.TotalBudgetUSD - bt.SpentUSD) / bt.TotalBudgetUSD) * 100
}

// ShouldDigDeeper returns true if the agent should continue exploring
// the current path (promising lead) vs pivoting to new paths.
// This is the core "BATS" (Budget Aware Test-time Scaling) decision function.
func (bt *BudgetTracker) ShouldDigDeeper() bool {
	if bt == nil || bt.TotalBudgetUSD <= 0 {
		return true // No budget constraint, always allow deep exploration
	}
	return bt.RemainingBudgetPct() > 20 // Only dig deeper if >20% budget remains
}

// Reset clears the budget tracking state while preserving the total budget.
// Useful for starting a new sub-task within the same session.
func (bt *BudgetTracker) Reset() {
	if bt == nil {
		return
	}
	bt.mu.Lock()
	defer bt.mu.Unlock()
	bt.SpentUSD = 0
	bt.ToolCallCount = 0
	bt.LastReset = time.Now()
}

// Stats returns current budget statistics for debugging/monitoring.
func (bt *BudgetTracker) Stats() map[string]interface{} {
	if bt == nil {
		return nil
	}
	bt.mu.Lock()
	defer bt.mu.Unlock()

	return map[string]interface{}{
		"total_budget_usd":   bt.TotalBudgetUSD,
		"spent_usd":          bt.SpentUSD,
		"remaining_usd":      bt.TotalBudgetUSD - bt.SpentUSD,
		"remaining_pct":      bt.RemainingBudgetPct(),
		"tool_call_count":    bt.ToolCallCount,
		"avg_cost_per_call":  bt.avgCostPerCall(),
		"elapsed_seconds":    time.Since(bt.StartTime).Seconds(),
		"last_reset_seconds": time.Since(bt.LastReset).Seconds(),
	}
}

func (bt *BudgetTracker) avgCostPerCall() float64 {
	if bt.ToolCallCount == 0 {
		return 0
	}
	return bt.SpentUSD / float64(bt.ToolCallCount)
}
