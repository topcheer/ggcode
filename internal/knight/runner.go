package knight

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
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

// RunTask executes a single Knight task with budget tracking and default maxTurns=10.
// Token usage is tracked via the onUsage callback wired into the agent.
func (k *Knight) RunTask(ctx context.Context, taskName, prompt string, factory AgentFactory) TaskResult {
	return k.RunTaskWithTurns(ctx, taskName, prompt, factory, 10)
}

// RunTaskWithTurns executes a single Knight task with a custom maxTurns limit.
func (k *Knight) RunTaskWithTurns(ctx context.Context, taskName, prompt string, factory AgentFactory, maxTurns int) TaskResult {
	start := time.Now()
	result := TaskResult{TaskName: taskName}

	if !k.budget.CanSpend() {
		result.Error = fmt.Errorf("daily budget exhausted")
		return result
	}
	bucket := classifyBucket(taskName)
	if !k.bucketBudget.canSpend(bucket, time.Now()) {
		result.Error = fmt.Errorf("bucket %q exhausted for today", bucket)
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
		k.bucketBudget.record(bucket, usage.InputTokens+usage.OutputTokens, time.Now())
	}

	// Create agent via factory
	runner, err := factory(sysPrompt, maxTurns, onUsage)
	if err != nil {
		result.Error = fmt.Errorf("create agent: %w", err)
		return result
	}

	// Run, collecting text output
	var output strings.Builder
	err = runner.RunStream(ctx, prompt, func(event provider.StreamEvent) {
		defer safego.Recover("knight.runner.streamCallback")
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
	// #1261: the generic "use the appropriate tools to create content" rule
	// below directly contradicted the proposal prompt's "do NOT modify the
	// project" instruction - and with the full interactive tool registry
	// injected, an LLM following the system prompt over the task prompt had a
	// real unauthorized-write path. Proposal tasks now get an explicit
	// read-only rule at system-prompt level (plus the post-run git check in
	// project_proposal.go, which catches violations regardless of adherence).
	readOnlyRules := ""
	if strings.Contains(taskName, "proposal") {
		readOnlyRules = `
READ-ONLY TASK: this is a proposal task.
- Do NOT modify, create, or delete any file in the project.
- Do NOT use edit_file, write_file, multi_edit_file, file_ops, or mutating run_command / git commands.
- Read-only inspection only. Your only output is the proposal document.`
	}
	return fmt.Sprintf(`You are Knight, a background agent that helps maintain and improve the project.
You run autonomously without direct user interaction.

Current task: %s
%s
Rules:
- Be thorough but concise
- If you discover issues, describe them clearly
- If you create content (skills, tests), use the appropriate tools
- Do not ask questions — make reasonable assumptions
- IMPORTANT: Your FINAL text output must be exactly the requested artifact (skill document, test code, etc.), NOT a summary or analysis of what you did. The user will only see your last text output.

For skill-generation tasks specifically:
- Your LAST message must be the complete skill document starting with ---
- Do NOT output any analysis, summary, or explanation after the skill document
	- Do NOT output any text before the --- frontmatter in your final message`, taskName, readOnlyRules)
}
