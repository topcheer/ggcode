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
	Duration time.Duration
	Error    error
}

// AgentRunner is the interface Knight uses to run LLM-powered tasks.
// It mirrors subagent.AgentRunner for compatibility.
type AgentRunner interface {
	RunStream(ctx context.Context, prompt string, onEvent func(provider.StreamEvent)) error
}

// AgentFactory creates a Knight agent with restricted tools.
type AgentFactory func(systemPrompt string, maxTurns int) (AgentRunner, error)

// RunTask executes a single Knight task with budget tracking.
// Token usage is tracked by the caller wiring the agent's onUsage callback
// to the budget — the runner itself focuses on orchestration.
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

	// Create agent via factory
	runner, err := factory(sysPrompt, 10) // max 10 turns for Knight tasks
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
	result.Duration = time.Since(start)
	result.Error = err

	debug.Log("knight", "task %s completed in %v", taskName, result.Duration)
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
