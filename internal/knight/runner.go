package knight

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// TaskResult holds the outcome of a Knight task execution.
type TaskResult struct {
	TaskName string
	Output   string
	Tokens   provider.TokenUsage
	Duration time.Duration
	Error    error
}

// AgentRunner is the interface Knight uses to run LLM-powered tasks.
// It mirrors subagent.AgentRunner for compatibility.
type AgentRunner interface {
	RunStream(ctx context.Context, prompt string, onEvent func(provider.StreamEvent)) error
}

// AgentFactory creates a Knight agent with restricted tools.
// The onUsage callback receives token usage after each LLM call.
type AgentFactory func(systemPrompt string, maxTurns int, onUsage func(provider.TokenUsage)) (AgentRunner, error)

// RunTask executes a single Knight task with budget tracking.
// Token usage is tracked via the onUsage callback wired into the agent.
func (k *Knight) RunTask(ctx context.Context, taskName, prompt string, factory AgentFactory) TaskResult {
	start := time.Now()
	result := TaskResult{TaskName: taskName}

	if !k.budget.CanSpend() {
		result.Error = fmt.Errorf("daily budget exhausted")
		return result
	}

	debug.Log("knight", "starting task: %s", taskName)

	// Build system prompt for Knight tasks
	sysPrompt := buildKnightSystemPrompt(taskName)

	// Wire up usage tracking — each LLM call's tokens go to budget
	var totalUsage provider.TokenUsage
	onUsage := func(usage provider.TokenUsage) {
		totalUsage.InputTokens += usage.InputTokens
		totalUsage.OutputTokens += usage.OutputTokens
		totalUsage.CacheRead += usage.CacheRead
		totalUsage.CacheWrite += usage.CacheWrite
		// Record immediately so budget stays current
		if err := k.budget.Record(taskName, usage.InputTokens, usage.OutputTokens); err != nil {
			debug.Log("knight", "budget record error: %v", err)
		}
	}

	// Create agent via factory
	runner, err := factory(sysPrompt, 10, onUsage)
	if err != nil {
		result.Error = fmt.Errorf("create agent: %w", err)
		return result
	}

	// Run, collecting text output
	var output strings.Builder
	err = runner.RunStream(ctx, prompt, func(event provider.StreamEvent) {
		switch event.Type {
		case provider.StreamEventText:
			output.WriteString(event.Text)
		case provider.StreamEventError:
			output.WriteString(fmt.Sprintf("[error: %v]\n", event.Error))
			debug.Log("knight", "task %s error: %v", taskName, event.Error)
		}
	})

	result.Output = output.String()
	result.Tokens = totalUsage
	result.Duration = time.Since(start)
	result.Error = err

	debug.Log("knight", "task %s completed in %v (tokens: in=%d out=%d)",
		taskName, result.Duration, totalUsage.InputTokens, totalUsage.OutputTokens)
	return result
}

// buildKnightSystemPrompt creates a system prompt for Knight tasks.
func buildKnightSystemPrompt(taskName string) string {
	return fmt.Sprintf(`You are Knight, a background agent that helps maintain and improve the project.
You run autonomously without direct user interaction.

Current task: %s

Rules:
- Be thorough but concise
- If you discover issues, describe them clearly
- If you create content (skills, tests), use the appropriate tools
- Do not ask questions — make reasonable assumptions
- Report your findings and actions clearly at the end`, taskName)
}
