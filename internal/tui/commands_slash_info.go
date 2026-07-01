package tui

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
	"github.com/topcheer/ggcode/internal/cost"
	"github.com/topcheer/ggcode/internal/hooks"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/util"
	"github.com/topcheer/ggcode/internal/version"
)

func (m *Model) listSessions() tea.Cmd {
	return func() tea.Msg {
		if m.sessionStore == nil {
			return streamMsg(m.t("session.store_missing"))
		}
		sessions, err := m.sessionStore.List()
		if err != nil {
			return streamMsg(m.t("session.list_failed", err))
		}
		if len(sessions) == 0 {
			return streamMsg(m.t("session.none"))
		}
		var b strings.Builder
		b.WriteString(m.t("session.list.title"))
		for i, s := range sessions {
			title := s.Title
			if title == "" {
				title = m.t("session.untitled")
			}
			updated := s.UpdatedAt.Format(time.RFC3339)
			b.WriteString(m.t("session.list.item", i+1, s.ID, title, updated))
		}
		b.WriteString(m.t("session.list.hint"))
		return streamMsg(b.String())
	}
}

func (m *Model) resumeSession(id string) tea.Cmd {
	return func() tea.Msg {
		if m.sessionStore == nil {
			return streamMsg(m.t("session.store_missing"))
		}
		ses, err := m.sessionStore.Load(id)
		return sessionResumeLoadedMsg{
			requestedID: id,
			session:     ses,
			err:         err,
		}
	}
}

func (m *Model) applyResumedSession(ses *session.Session) {
	if ses == nil {
		return
	}
	if m.agent != nil {
		m.agent.Clear()
		for _, msg := range ses.Messages {
			m.agent.AddMessage(msg)
		}
	}
	m.SetSession(ses, m.sessionStore)
	m.rebuildConversationFromMessages(ses.Messages)
	m.restoreHistoryFromMessages(ses.Messages)
}

func publishCurrentSessionCmd(reset bool) tea.Cmd {
	return func() tea.Msg {
		return tunnelPublishCurrentSessionMsg{reset: reset}
	}
}

func (m *Model) exportSession(id string) tea.Cmd {
	return func() tea.Msg {
		if m.sessionStore == nil {
			return streamMsg(m.t("session.store_missing"))
		}
		md, err := m.sessionStore.ExportMarkdown(id)
		if err != nil {
			return streamMsg(m.t("session.export_failed", err))
		}
		filename := fmt.Sprintf("session-%s.md", id)
		if err := os.WriteFile(filename, []byte(md), 0644); err != nil {
			return streamMsg(m.t("session.write_failed", err))
		}
		return streamMsg(m.t("session.exported", id, filename))
	}
}

func (m *Model) handleCheckpointsCommand() tea.Cmd {
	m.openInspectorPanel(inspectorPanelCheckpoints)
	return nil
}

func (m *Model) handleMemoryCommand(parts []string) tea.Cmd {
	sub := ""
	if len(parts) > 1 {
		sub = strings.ToLower(parts[1])
	}
	switch sub {
	case "list":
		m.openInspectorPanel(inspectorPanelMemory)
	case "clear":
		if m.autoMem == nil {
			m.chatWriteSystem(nextSystemID(), m.t("memory.auto_unavailable"))
			return nil
		}
		if err := m.autoMem.Clear(); err != nil {
			m.chatWriteSystem(nextSystemID(), m.t("memory.clear_failed", err))
			return nil
		}
		m.chatWriteSystem(nextSystemID(), m.t("memory.cleared"))
	default:
		m.openInspectorPanel(inspectorPanelMemory)
	}
	return nil
}

func (m *Model) handleBugCommand() tea.Cmd {
	return func() tea.Msg {
		var b strings.Builder
		b.WriteString(m.t("bug.title"))

		// Version info
		b.WriteString(m.t("bug.version", version.Display()))
		b.WriteString(m.t("bug.os", runtime.GOOS, runtime.GOARCH))
		b.WriteString(m.t("bug.go", runtime.Version()))

		// Config info
		if m.config != nil {
			b.WriteString(m.t("bug.provider", m.config.Vendor))
			b.WriteString(m.t("bug.model", m.config.Model))
		}

		// Session info
		if m.session != nil {
			b.WriteString(m.t("bug.session", m.session.ID, len(m.session.Messages)))
		}

		// MCP info
		if len(m.mcpServers) > 0 {
			b.WriteString(m.t("bug.mcp", len(m.mcpServers)))
		}

		// Recent errors from chatList items
		if m.chatList != nil {
			for i := m.chatList.Len() - 1; i >= 0; i-- {
				item := m.chatList.ItemAt(i)
				text := stripAnsiForChat(item.Render(200))
				if idx := strings.LastIndex(text, "Error:"); idx >= 0 {
					end := idx + 500
					if end > len(text) {
						end = len(text)
					}
					b.WriteString(m.t("bug.last_error", text[idx:end]))
					break
				}
			}
		}

		b.WriteString(m.t("bug.hint"))
		return streamMsg(b.String())
	}
}

func (m *Model) handleStatusCommand() tea.Cmd {
	m.openInspectorPanel(inspectorPanelStatus)
	return nil
}

func (m *Model) handlePluginsCommand() tea.Cmd {
	m.openInspectorPanel(inspectorPanelPlugins)
	return nil
}

// handleInspectorCommand opens the inspector panel for the requested sub-panel.
// With no argument, defaults to status.
func (m *Model) handleInspectorCommand(parts []string) tea.Cmd {
	kind := inspectorPanelStatus // default
	if len(parts) > 1 {
		switch strings.ToLower(parts[1]) {
		case "sessions", "session", "s":
			kind = inspectorPanelSessions
		case "checkpoints", "checkpoint", "c":
			kind = inspectorPanelCheckpoints
		case "memory", "mem":
			kind = inspectorPanelMemory
		case "todos", "todo", "t":
			kind = inspectorPanelTodos
		case "plugins", "plugin", "p":
			kind = inspectorPanelPlugins
		case "config", "cfg":
			kind = inspectorPanelConfig
		case "status", "stat":
			kind = inspectorPanelStatus
		}
	}
	m.openInspectorPanel(kind)
	return nil
}

func (m *Model) handleMCPCommand() tea.Cmd {
	if len(m.mcpServers) == 0 {
		m.chatWriteSystem(nextSystemID(), m.t("mcp.none"))
		return nil
	}
	m.openMCPPanel()
	return nil
}

// handleDiffCommand runs `git diff` and displays the output in the chat.
// Supports: /diff, /diff --cached, /diff <file>, /diff --stat
func (m *Model) handleDiffCommand(parts []string) tea.Cmd {
	args := []string{"diff"}
	if len(parts) > 1 {
		args = append(args, parts[1:]...)
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = workingDirFromModel(m)
	output, err := cmd.CombinedOutput()
	if err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("git diff error: %v", err))
		return nil
	}

	result := strings.TrimSpace(string(output))
	if result == "" {
		m.chatWriteSystem(nextSystemID(), "No changes detected (working tree clean).")
		return nil
	}

	// Truncate very large diffs
	maxLines := 200
	lines := strings.Split(result, "\n")
	if len(lines) > maxLines {
		result = strings.Join(lines[:maxLines], "\n") +
			fmt.Sprintf("\n\n... (%d more lines, run /diff <file> to narrow)", len(lines)-maxLines)
	}

	m.chatWriteSystem(nextSystemID(), "```\n"+result+"\n```")
	return nil
}

// handleHooksCommand displays the current hook configuration in the chat.
func (m *Model) handleHooksCommand() tea.Cmd {
	cfg := hooks.HookConfig{}
	if m.agent != nil {
		cfg = m.agent.GetHookConfig()
	}

	var sb strings.Builder
	sb.WriteString("Hooks:\n\n")

	events := []struct {
		name  string
		hooks []hooks.Hook
	}{
		{"on_user_message", cfg.OnUserMessage},
		{"pre_tool_use", cfg.PreToolUse},
		{"post_tool_use", cfg.PostToolUse},
		{"on_agent_stop", cfg.OnAgentStop},
		{"on_stream_stop", cfg.OnStreamStop},
	}

	total := 0
	for _, ev := range events {
		if len(ev.hooks) == 0 {
			continue
		}
		sb.WriteString(fmt.Sprintf("%s (%d):\n", ev.name, len(ev.hooks)))
		for i, h := range ev.hooks {
			hookType := h.HasType()
			detail := ""
			switch hookType {
			case hooks.HookTypeHTTP:
				detail = fmt.Sprintf("url=%s", h.URL)
			default:
				detail = fmt.Sprintf("cmd=%s", h.Command)
			}
			inject := ""
			if h.InjectOutput {
				inject = " [inject]"
			}
			sb.WriteString(fmt.Sprintf("  [%d] %s | %s | match=%q%s\n", i, hookType, detail, h.Match, inject))
			total++
		}
		sb.WriteString("\n")
	}

	if total == 0 {
		sb.WriteString("(no hooks configured — see ggcode.example.yaml for examples)")
	}

	// Show validation errors if any
	if errs := hooks.ValidateHooks(cfg); len(errs) > 0 {
		sb.WriteString("\n⚠ Validation errors:\n")
		for _, e := range errs {
			sb.WriteString("  - " + e + "\n")
		}
	}

	m.chatWriteSystem(nextSystemID(), sb.String())
	return nil
}

// handleCostCommand displays the session token usage and estimated cost.
func (m *Model) handleCostCommand() tea.Cmd {
	if m.session == nil {
		m.chatWriteSystem(nextSystemID(), "No active session.")
		return nil
	}

	usage := m.session.TokenUsage
	if usage.Total() == 0 {
		usage = m.sidebarSessionUsage()
	}

	if usage.Total() == 0 {
		m.chatWriteSystem(nextSystemID(), "No token usage recorded yet for this session.")
		return nil
	}

	model := m.session.Model
	vendor := m.session.Vendor

	var sb strings.Builder
	sb.WriteString("Session Cost Breakdown:\n\n")
	sb.WriteString(fmt.Sprintf("  Model:  %s (%s)\n\n", model, vendor))
	sb.WriteString(fmt.Sprintf("  Input tokens:       %s\n", humanizeTokenCount(usage.InputTokens)))
	sb.WriteString(fmt.Sprintf("  Output tokens:      %s\n", humanizeTokenCount(usage.OutputTokens)))
	if usage.CacheRead > 0 {
		sb.WriteString(fmt.Sprintf("  Cache read:         %s\n", humanizeTokenCount(usage.CacheRead)))
	}
	if usage.CacheWrite > 0 {
		sb.WriteString(fmt.Sprintf("  Cache write:        %s\n", humanizeTokenCount(usage.CacheWrite)))
	}
	sb.WriteString(fmt.Sprintf("  Total tokens:       %s\n\n", humanizeTokenCount(usage.Total())))

	// Estimate cost using pricing table
	endpoint := m.session.Endpoint
	rate := resolveRate(vendor, endpoint, model)

	if rate.IsKnown() {
		if !rate.IsMetered() {
			planLabel := rate.Plan
			if planLabel == "" {
				planLabel = string(rate.Type)
			}
			sb.WriteString(fmt.Sprintf("  Estimated cost:     included in %s\n", planLabel))
			sb.WriteString(fmt.Sprintf("  (billing: %s, not per-token)\n", rate.Type))
		} else {
			estimatedCost := float64(usage.InputTokens)*rate.InputPerM/1e6 +
				float64(usage.OutputTokens)*rate.OutputPerM/1e6 +
				float64(usage.CacheRead)*rate.CacheReadPerM/1e6 +
				float64(usage.CacheWrite)*rate.CacheWritePerM/1e6
			sb.WriteString(fmt.Sprintf("  Estimated cost:     $%.4f\n", estimatedCost))
			sb.WriteString(fmt.Sprintf("  (rate: $%.2f/M in, $%.2f/M out)\n", rate.InputPerM, rate.OutputPerM))
		}
	} else {
		sb.WriteString("  Estimated cost:     (no pricing data available)\n")
		sb.WriteString("  (configure custom rates via pricing table Merge())\n")
	}

	m.chatWriteSystem(nextSystemID(), sb.String())
	return nil
}

// resolveRate determines the billing type for a session by checking:
// 1. Explicit pricing table entry (vendor + model)
// 2. Coding plan endpoint pattern (e.g., "cn-coding-openai")
// 3. Subscription vendor (all endpoints are subscription)
// 4. Falls back to PricingUnknown
func resolveRate(vendor, endpoint, model string) cost.ModelRate {
	pt := cost.DefaultPricingTable()

	// 1. Check explicit pricing table first (covers github-copilot, glm-4.5-air free)
	if rate, ok := pt.Get(vendor, model); ok {
		return rate
	}

	// 2. Coding plan endpoint → subscription
	if cost.IsCodingPlanEndpoint(endpoint) {
		return cost.ModelRate{
			Type: cost.PricingSubscription,
			Plan: "Coding Plan",
		}
	}

	// 3. Entirely subscription vendor (kimi, ark, aliyun, minimax, xiaomi-mimo)
	if plan := cost.IsSubscriptionVendor(vendor); plan != "" {
		return cost.ModelRate{
			Type: cost.PricingSubscription,
			Plan: plan,
		}
	}

	// 4. Unknown — no hardcoded per-token prices
	return cost.ModelRate{}
}

// handleReviewCommand runs a code review on the current working tree changes.
// It gathers the git diff and sends it to the agent with a structured review prompt.
// Supports: /review, /review --cached, /review --staged
func (m *Model) handleReviewCommand(parts []string) tea.Cmd {
	diffArgs := []string{"diff"}
	if len(parts) > 1 {
		for _, p := range parts[1:] {
			if p == "--staged" {
				p = "--cached"
			}
			diffArgs = append(diffArgs, p)
		}
	}

	return func() tea.Msg {
		// Get diff
		cmd := exec.Command("git", diffArgs...)
		cmd.Dir = workingDirFromModel(m)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return streamMsg(fmt.Sprintf("git diff error: %v", err))
		}

		diff := strings.TrimSpace(string(output))
		if diff == "" {
			return streamMsg("No changes to review (working tree clean).")
		}

		// Truncate very large diffs for the prompt
		maxLines := 500
		lines := strings.Split(diff, "\n")
		truncated := false
		if len(lines) > maxLines {
			diff = strings.Join(lines[:maxLines], "\n")
			truncated = true
		}

		// Build review prompt
		var prompt strings.Builder
		prompt.WriteString("Review the following git diff. Focus on:\n")
		prompt.WriteString("1. **Bugs**: Logic errors, edge cases, nil dereferences, off-by-one\n")
		prompt.WriteString("2. **Security**: Injection, path traversal, secrets in code\n")
		prompt.WriteString("3. **Race conditions**: Concurrent access without proper locking\n")
		prompt.WriteString("4. **Resource leaks**: Unclosed files, goroutine leaks, missing defers\n")
		prompt.WriteString("5. **API consistency**: Breaking changes, missing error handling\n\n")
		prompt.WriteString("Be concise. Only mention issues that matter — skip style nits.\n")
		prompt.WriteString("Format: `<severity> <file>:<line> — <issue>` (severity: critical/high/medium/low)\n")
		prompt.WriteString("If no issues found, say \"LGTM\" and explain why the changes look correct.\n\n")
		prompt.WriteString("```diff\n")
		prompt.WriteString(diff)
		prompt.WriteString("\n```")
		if truncated {
			prompt.WriteString(fmt.Sprintf("\n\n(diff truncated: showing first %d of %d lines)", maxLines, len(lines)))
		}

		return reviewReadyMsg{text: prompt.String()}
	}
}

// reviewReadyMsg carries the expanded review text to be sent to the agent.
type reviewReadyMsg struct {
	text string
}

// handleCopyCommand copies the last assistant response to the system clipboard.
// Usage: /copy  — copies the most recent agent reply (markdown source, not rendered).
func (m *Model) handleCopyCommand() tea.Cmd {
	return func() tea.Msg {
		if m.chatList == nil {
			return streamMsg("Chat not initialized.")
		}
		text := m.chatList.LastAssistantText()
		if strings.TrimSpace(text) == "" {
			return streamMsg("No assistant response to copy.")
		}
		if err := clipboard.WriteAll(text); err != nil {
			return streamMsg(fmt.Sprintf("Clipboard error: %v", err))
		}
		preview := util.Truncate(strings.TrimSpace(text), 60)
		return streamMsg(fmt.Sprintf("Copied %d chars to clipboard: %s", len(text), preview))
	}
}

// handleContextCommand shows a detailed breakdown of the context window usage,
// including system prompt size, conversation messages, token distribution,
// and proximity to auto-compaction threshold.
func (m *Model) handleContextCommand() tea.Cmd {
	return func() tea.Msg {
		if m.agent == nil {
			return streamMsg("Agent not initialized.")
		}

		cm := m.agent.ContextManager()
		if cm == nil {
			return streamMsg("Context manager not available.")
		}

		ctxWindow := cm.ContextWindow()
		if ctxWindow <= 0 {
			return streamMsg("Context window size unknown.")
		}

		messages := cm.Messages()
		totalTokens := cm.TokenCount()
		usageRatio := cm.UsageRatio()
		compactThreshold := cm.AutoCompactThreshold()

		// System prompt token estimate
		sysPrompt := m.agent.SystemPrompt()
		sysTokens := estimateTokens(sysPrompt)

		// Count messages by role
		var userMsgs, asstMsgs, toolMsgs int
		var userTokens, asstTokens, toolTokens int
		for _, msg := range messages {
			msgTokens := estimateMessageTokens(msg)
			switch msg.Role {
			case "user":
				userMsgs++
				userTokens += msgTokens
			case "assistant":
				asstMsgs++
				asstTokens += msgTokens
			case "tool":
				toolMsgs++
				toolTokens += msgTokens
			}
		}

		conversationTokens := userTokens + asstTokens + toolTokens
		remaining := ctxWindow - totalTokens
		if remaining < 0 {
			remaining = 0
		}

		// Progress bar
		pct := usageRatio * 100
		barWidth := 20
		filled := int(usageRatio * float64(barWidth))
		if filled > barWidth {
			filled = barWidth
		}
		var bar string
		for i := 0; i < barWidth; i++ {
			if i < filled {
				bar += "█"
			} else {
				bar += "░"
			}
		}

		var sb strings.Builder
		sb.WriteString("```\n")
		sb.WriteString("Context Window Usage\n\n")
		sb.WriteString(fmt.Sprintf("  %s %.1f%%\n", bar, pct))
		sb.WriteString(fmt.Sprintf("  %s / %s tokens\n\n", humanizeTokenCount(totalTokens), humanizeTokenCount(ctxWindow)))

		sb.WriteString("Breakdown:\n")
		sb.WriteString(fmt.Sprintf("  System prompt:   %s (%d chars)\n", humanizeTokenCount(sysTokens), len(sysPrompt)))
		sb.WriteString(fmt.Sprintf("  User messages:   %d msgs, %s tokens\n", userMsgs, humanizeTokenCount(userTokens)))
		sb.WriteString(fmt.Sprintf("  Assistant msgs:  %d msgs, %s tokens\n", asstMsgs, humanizeTokenCount(asstTokens)))
		if toolMsgs > 0 {
			sb.WriteString(fmt.Sprintf("  Tool results:    %d msgs, %s tokens\n", toolMsgs, humanizeTokenCount(toolTokens)))
		}
		sb.WriteString(fmt.Sprintf("  Conversation:    %s tokens\n\n", humanizeTokenCount(conversationTokens)))

		sb.WriteString("Capacity:\n")
		sb.WriteString(fmt.Sprintf("  Remaining:       %s tokens\n", humanizeTokenCount(remaining)))
		compactPct := float64(compactThreshold) / float64(ctxWindow) * 100
		sb.WriteString(fmt.Sprintf("  Auto-compact at: %s tokens (%.0f%%)\n", humanizeTokenCount(compactThreshold), compactPct))
		tokensUntilCompact := compactThreshold - totalTokens
		if tokensUntilCompact > 0 {
			sb.WriteString(fmt.Sprintf("  Until compact:   %s tokens\n", humanizeTokenCount(tokensUntilCompact)))
		} else {
			sb.WriteString("  ! Compaction threshold exceeded - will compact on next turn\n")
		}
		sb.WriteString("```")

		return streamMsg(sb.String())
	}
}

// estimateTokens provides a rough token estimate (4 chars ≈ 1 token).
func estimateTokens(text string) int {
	if len(text) == 0 {
		return 0
	}
	return (len(text) + 3) / 4
}

// estimateMessageTokens sums token estimates across all content blocks.
func estimateMessageTokens(msg provider.Message) int {
	var total int
	for _, block := range msg.Content {
		switch block.Type {
		case "text":
			total += estimateTokens(block.Text)
		case "image":
			// Vision tokens are larger; approximate as 1000 per image
			total += 1000
		default:
			total += 100
		}
	}
	return total
}
