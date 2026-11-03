package subagent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	runtimedebug "runtime/debug"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/util"
)

const subagentRedactedReasoningPlaceholder = "Reasoning hidden by model."

type ToolInfo interface {
	Name() string
}

type AgentFactory func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) AgentRunner

type AgentRunner interface {
	RunStream(ctx context.Context, prompt string, onEvent func(provider.StreamEvent), origin ...string) error
}

type usageHandlerSetter interface {
	SetUsageHandler(func(provider.TokenUsage))
}

type RunnerConfig struct {
	Provider            provider.Provider
	AllTools            []ToolInfo
	Task                string
	AllowedTools        []string
	Manager             *Manager
	SubAgentID          string
	AgentFactory        AgentFactory
	BuildToolSet        func(allowedTools []string, allTools []ToolInfo) interface{}
	Model               string
	AgentType           string
	WorkingDir          string
	OnStreamText        func(agentID, text string)
	OnUsage             func(provider.TokenUsage)
	OnMetric            func(metrics.MetricEvent)
	SystemPromptBuilder func(task, agentType string) string
}

func Run(ctx context.Context, cfg RunnerConfig) {

	if err := cfg.Manager.acquireSlot(ctx, cfg.SubAgentID); err != nil {
		cfg.Manager.Complete(cfg.SubAgentID, "", fmt.Errorf("failed to acquire slot: %w", err))
		return
	}
	defer cfg.Manager.releaseSlot(cfg.SubAgentID)

	defer func() {
		if r := recover(); r != nil {
			debug.Log("subagent", "panic recovered subagent=%s error=%v stack=%s", cfg.SubAgentID, r, string(runtimedebug.Stack()))
			cfg.Manager.Complete(cfg.SubAgentID, "", fmt.Errorf("sub-agent panic: %v", r))
		}
	}()

	timeout := cfg.Manager.Timeout()
	subCtx, cancel := context.WithTimeout(ctx, timeout)

	subCtx = util.WithActor(subCtx, cfg.SubAgentID)
	defer cancel()
	if !cfg.Manager.SetCancel(cfg.SubAgentID, cancel) {

		cfg.Manager.Complete(cfg.SubAgentID, "", context.Canceled)
		return
	}

	sa, _ := cfg.Manager.Get(cfg.SubAgentID)
	if sa != nil {
		sa.setStatus(StatusRunning)
		sa.setActivity("thinking", "", "")
		sa.setStartedAt(time.Now())

		if cfg.Model != "" {
			sa.SetModel(cfg.Model)
		}

		sa.mu.Lock()
		sa.goroutineStarted = true
		sa.mu.Unlock()
		debug.Log("cancel", "Run: goroutine started id=%s", cfg.SubAgentID)
	}

	var toolSet interface{}
	if cfg.BuildToolSet != nil {
		toolSet = cfg.BuildToolSet(cfg.AllowedTools, cfg.AllTools)
	}

	var systemPrompt string
	if cfg.SystemPromptBuilder != nil {
		systemPrompt = cfg.SystemPromptBuilder(cfg.Task, cfg.AgentType)
	} else {
		rolePrefix := "You are a sub-agent."
		if cfg.AgentType != "" {
			rolePrefix = fmt.Sprintf("You are a %s sub-agent.", cfg.AgentType)
		}
		systemPrompt = fmt.Sprintf(
			"%s Complete the following task independently:\n%s\n\nProvide a concise result. Do not spawn further agents.",
			rolePrefix,
			cfg.Task,
		)
		if cfg.WorkingDir != "" {
			systemPrompt += fmt.Sprintf("\n\nWorking directory: %s", cfg.WorkingDir)
		}
	}
	if cfg.AgentFactory == nil {
		cfg.Manager.Complete(cfg.SubAgentID, "", fmt.Errorf("AgentFactory not configured"))
		return
	}

	runProv := cfg.Provider
	if cfg.Model != "" {
		runProv = provider.CloneProviderWithModel(cfg.Provider, cfg.Model)
		debug.Log("subagent", "Run: model override id=%s model=%s", cfg.SubAgentID, cfg.Model)
	}
	subAgent := cfg.AgentFactory(runProv, toolSet, systemPrompt, 0)

	if cfg.WorkingDir != "" {
		if wd, ok := subAgent.(interface{ SetWorkingDir(string) }); ok {
			wd.SetWorkingDir(cfg.WorkingDir)
		}
	}

	if cfg.SubAgentID != "" {
		if sid, ok := subAgent.(interface{ SetSessionID(string) }); ok {
			sid.SetSessionID(cfg.SubAgentID)
		}
	}
	if cfg.OnUsage != nil {
		if usageAware, ok := subAgent.(usageHandlerSetter); ok {
			usageAware.SetUsageHandler(cfg.OnUsage)
		}
	}

	if cfg.OnMetric != nil {
		if metricAware, ok := subAgent.(interface {
			SetMetricHandler(func(metrics.MetricEvent))
		}); ok {
			id := cfg.SubAgentID
			onMetric := cfg.OnMetric
			metricAware.SetMetricHandler(func(m metrics.MetricEvent) {
				m.AgentID = id
				onMetric(m)
			})
		}
	}

	var output strings.Builder
	type pendingToolMeta struct {
		Name        string
		RawArgs     string
		DisplayName string
		Detail      string
	}
	pendingTools := make(map[string]pendingToolMeta)

	var unnamedTools []pendingToolMeta
	var textBuf strings.Builder
	flushText := func() {
		if textBuf.Len() == 0 {
			return
		}
		text := textBuf.String()
		textBuf.Reset()
		if sa, ok := cfg.Manager.Get(cfg.SubAgentID); ok {
			sa.appendEvent(AgentEvent{Type: AgentEventText, Text: text})
		}
	}
	err := subAgent.RunStream(subCtx, cfg.Task, func(event provider.StreamEvent) {
		switch event.Type {
		case provider.StreamEventText:
			output.WriteString(event.Text)
			textBuf.WriteString(event.Text)
			if sa, ok := cfg.Manager.Get(cfg.SubAgentID); ok {
				sa.setActivity("writing", "", "")
			}
			cfg.Manager.NotifyStreamText(cfg.SubAgentID, event.Text)

			if textBuf.Len() >= 800 {
				flushText()
				cfg.Manager.Notify(cfg.SubAgentID)
			}
		case provider.StreamEventToolCallDone:

			flushText()
			rawArgs := string(event.Tool.Arguments)
			meta := pendingToolMeta{
				Name:    event.Tool.Name,
				RawArgs: rawArgs,
			}

			if sa, ok := cfg.Manager.Get(cfg.SubAgentID); ok {
				sa.IncrementToolCalls()

				if isExternalSourceTool(meta.Name) {
					sa.RecordExternalSource(meta.Name)
				}
				sa.setActivity("tool", meta.Name, meta.RawArgs)
				sa.setToolExecuting(true)
				sa.appendEvent(AgentEvent{
					Type:     AgentEventToolCall,
					ToolName: meta.Name,
					ToolID:   event.Tool.ID,
					ToolArgs: meta.RawArgs,
				})
			}
			cfg.Manager.Notify(cfg.SubAgentID)
			cfg.Manager.NotifyToolCall(cfg.SubAgentID, event.Tool.ID, meta.Name, "", meta.RawArgs, "")
			if event.Tool.ID != "" {
				pendingTools[event.Tool.ID] = meta
			} else {
				unnamedTools = append(unnamedTools, meta)
			}
		case provider.StreamEventToolResult:
			flushText()
			meta, ok := pendingTools[event.Tool.ID]
			if ok {
				delete(pendingTools, event.Tool.ID)
			} else if event.Tool.ID == "" && len(unnamedTools) > 0 {
				meta = unnamedTools[0]
				unnamedTools = unnamedTools[1:]
			}
			if summary := subagentToolProgressSummary(meta.Name, event.Result); summary != "" {
				cfg.Manager.UpdateProgress(cfg.SubAgentID, summary)
			} else {
				cfg.Manager.Notify(cfg.SubAgentID)
			}
			if sa, ok := cfg.Manager.Get(cfg.SubAgentID); ok {
				sa.setToolExecuting(false)
				sa.appendEvent(AgentEvent{
					Type:     AgentEventToolResult,
					ToolName: meta.Name,
					ToolID:   event.Tool.ID,
					ToolArgs: meta.RawArgs,
					Result:   event.Result,
					IsError:  event.IsError,
				})
			}
			cfg.Manager.NotifyToolResult(cfg.SubAgentID, event.Tool.ID, meta.Name, "", "", event.Result, event.IsError)
		case provider.StreamEventToolCallChunk:

			if sa, ok := cfg.Manager.Get(cfg.SubAgentID); ok {
				sa.refreshActivity()
			}
		case provider.StreamEventSystem:

			agentName := cfg.SubAgentID
			if sa, ok := cfg.Manager.Get(cfg.SubAgentID); sa != nil && ok {
				if sa.Name != "" {
					agentName = sa.Name
				}
			}
			cfg.Manager.NotifySystem(cfg.SubAgentID, fmt.Sprintf("[%s] %s", agentName, event.Text))
		case provider.StreamEventError:
			flushText()
			output.WriteString(fmt.Sprintf("[error: %v]\n", event.Error))
			if sa, ok := cfg.Manager.Get(cfg.SubAgentID); ok {
				sa.setActivity("failed", "", "")
				sa.appendEvent(AgentEvent{
					Type:    AgentEventError,
					Text:    fmt.Sprintf("%v", event.Error),
					IsError: true,
				})
			}
			cfg.Manager.Notify(cfg.SubAgentID)
		case provider.StreamEventReasoning:

			text := event.Text
			if text == "" {
				break
			}
			if text == "__redacted_thinking__" {
				text = subagentRedactedReasoningPlaceholder
			}
			if sa, ok := cfg.Manager.Get(cfg.SubAgentID); ok {
				sa.setActivity("reasoning", "", "")
				sa.appendEvent(AgentEvent{
					Type: AgentEventReasoning,
					Text: text,
				})
			}
			cfg.Manager.NotifyReasoning(cfg.SubAgentID, text)
		}
	})

	flushText()

	result := output.String()

	if len(result) > maxSubAgentResultBytes {
		// #4792 (S-22): the full text used to be discarded after the
		// head+tail splice - review/closure flows that need verbatim
		// receipts then burned a whole extra subagent just to re-read the
		// output compactly. Persist the FULL result to a receipt file and
		// point the truncation marker at it. Best-effort: a write failure
		// keeps the old behavior (inline truncation, no receipt line).
		full := result
		receiptPath := writeFullResultReceipt(cfg.SubAgentID, full)
		marker := fmt.Sprintf("\n\n[... truncated: %d bytes total, showing head + tail ...]\n\n", len(full))
		if receiptPath != "" {
			marker += fmt.Sprintf("[full receipt: %s - read it with read_file when a verbatim echo is required]\n\n", receiptPath)
		}
		budget := maxSubAgentResultBytes - len(marker)
		if budget >= 1000 {
			headEnd := util.SnapToRuneStart(result, budget/2)
			tailStart := util.SnapToRuneStart(result, len(result)-budget/2)
			if headEnd < tailStart {
				result = result[:headEnd] + marker + result[tailStart:]
			} else {

				result = result[util.SnapToRuneStart(result, len(result)-budget):]
			}
		} else {

			result = result[util.SnapToRuneStart(result, len(result)-maxSubAgentResultBytes):]
		}
		debug.Log("subagent", "Run: result truncated id=%s total=%d cap=%d receipt=%s",
			cfg.SubAgentID, len(output.String()), maxSubAgentResultBytes, receiptPath)
	}

	todoPath := filepath.Join(config.HomeDir(), ".ggcode", "todos", cfg.SubAgentID+".json")
	os.Remove(todoPath)

	if err != nil {
		if subCtx.Err() == context.DeadlineExceeded {
			cfg.Manager.Complete(cfg.SubAgentID, result, fmt.Errorf("sub-agent timed out after %v", timeout))
		} else if subCtx.Err() == context.Canceled {
			debug.Log("cancel", "Run: context cancelled, calling Complete id=%s", cfg.SubAgentID)
			cfg.Manager.Complete(cfg.SubAgentID, result, fmt.Errorf("sub-agent cancelled"))
		} else {
			debug.Log("subagent", "Run: failed id=%s error=%v", cfg.SubAgentID, err)
			cfg.Manager.Complete(cfg.SubAgentID, result, err)
		}
	} else {
		debug.Log("cancel", "Run: completed successfully, calling Complete id=%s", cfg.SubAgentID)
		cfg.Manager.Complete(cfg.SubAgentID, result, nil)
	}
}

// writeFullResultReceipt persists an over-cap sub-agent result verbatim to
// <home>/.ggcode/subagent-results/<id>.txt and returns the path ("" on any
// failure - the receipt channel is best-effort and never blocks completion).
// #4792 (S-22).
func writeFullResultReceipt(subAgentID, full string) string {
	if subAgentID == "" || full == "" {
		return ""
	}
	dir := filepath.Join(config.HomeDir(), ".ggcode", "subagent-results")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	path := filepath.Join(dir, subAgentID+".txt")
	if err := os.WriteFile(path, []byte(full), 0o644); err != nil {
		return ""
	}
	return path
}

func Wait(ctx context.Context, mgr *Manager, id string) (string, error) {
	sa, ok := mgr.Get(id)
	if !ok {
		return "", fmt.Errorf("sub-agent %s not found", id)
	}

	done := sa.doneChan()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		sa.mu.Lock()
		status := sa.Status
		result := sa.Result
		err := sa.Error
		sa.mu.Unlock()

		switch status {
		case StatusCompleted:
			return result, nil
		case StatusFailed, StatusCancelled:

			if done != nil {
				select {
				case <-done:
				case <-ctx.Done():
					return "", ctx.Err()
				}
				grace := time.NewTimer(200 * time.Millisecond)
				select {
				case <-grace.C:
				case <-ctx.Done():
					grace.Stop()
					return "", ctx.Err()
				}
			}

			sa.mu.Lock()
			result = sa.Result
			err = sa.Error
			sa.mu.Unlock()
			return result, err
		}

		select {
		case <-ticker.C:
			continue
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}

type SnapshotProgressFunc func(summary string)

func WaitForSnapshot(ctx context.Context, mgr *Manager, id string, wait time.Duration) (Snapshot, error) {
	return WaitForSnapshotWithProgress(ctx, mgr, id, wait, nil)
}

func WaitForSnapshotWithProgress(ctx context.Context, mgr *Manager, id string, wait time.Duration, progressFn SnapshotProgressFunc) (Snapshot, error) {
	sa, ok := mgr.Get(id)
	if !ok {
		return Snapshot{}, fmt.Errorf("sub-agent %s not found", id)
	}

	if wait <= 0 {
		return sa.snapshot(), nil
	}

	timer := time.NewTimer(wait)
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var lastSummary string
	var lastEmit time.Time

	for {
		snap := sa.snapshot()
		switch snap.Status {
		case StatusCompleted, StatusFailed, StatusCancelled:
			return snap, nil
		}

		if progressFn != nil {
			summary := formatProgressSummary(snap)
			now := time.Now()
			if summary != lastSummary || now.Sub(lastEmit) >= 1*time.Second {
				progressFn(summary)
				lastSummary = summary
				lastEmit = now
			}
		}

		select {
		case <-ticker.C:
			continue
		case <-timer.C:
			return sa.snapshot(), nil
		case <-ctx.Done():
			return Snapshot{}, ctx.Err()
		}
	}
}

func formatProgressSummary(snap Snapshot) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[%s] ", snap.Status))
	if snap.ToolCallCount > 0 {
		sb.WriteString(fmt.Sprintf("%d tools", snap.ToolCallCount))
	}
	if snap.CurrentTool != "" {
		sb.WriteString(fmt.Sprintf(" · %s", snap.CurrentTool))
	}
	if snap.CurrentPhase != "" {
		sb.WriteString(fmt.Sprintf(" · %s", snap.CurrentPhase))
	}
	if summary := strings.TrimSpace(snap.ProgressSummary); summary != "" {

		sb.WriteString(fmt.Sprintf("\n  %s", util.Truncate(summary, 80)))
	}
	return sb.String()
}

func subagentToolProgressSummary(toolName, result string) string {
	switch toolName {
	case "start_command", "read_command_output", "wait_command", "stop_command", "write_command_input", "list_commands":
		return compactProgressSummary(result)
	default:
		return ""
	}
}

func compactProgressSummary(result string) string {
	lines := strings.Split(result, "\n")
	parts := make([]string, 0, 3)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Job ID: "):
			parts = append(parts, line)
		case strings.HasPrefix(line, "Status: "):
			parts = append(parts, line)
		case strings.HasPrefix(line, "Total lines: "):
			parts = append(parts, line)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " • ")
}
