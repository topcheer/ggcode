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
	"github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/cost"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
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

	// Guard: don't switch sessions while agent is running. Clearing the agent
	// context mid-run would cause undefined behavior.
	if m.loading {
		m.chatWriteSystem(nextSystemID(), m.t("session.switch_blocked_running"))
		return
	}

	// Save the session we are leaving (if different from target) so unsaved
	// usage/metrics messages are not lost.
	if m.session != nil && m.session.ID != ses.ID && m.sessionStore != nil {
		oldSes := m.session
		oldStore := m.sessionStore
		safego.Go("tui.applyResumedSession.saveOld", func() { _ = oldStore.Save(oldSes) })
	}

	// Delegate to the shared session-switching helper (isNew=false for resumed sessions).
	m.switchToSession(ses, false)
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

func (m *Model) handleFilesCommand() tea.Cmd {
	if m.agent == nil {
		return func() tea.Msg {
			return streamMsg(m.t("files.disabled"))
		}
	}
	cpMgr := m.agent.CheckpointManager()
	if cpMgr == nil {
		return func() tea.Msg {
			return streamMsg(m.t("files.disabled"))
		}
	}
	files := cpMgr.ModifiedFiles()
	if len(files) == 0 {
		return func() tea.Msg {
			return streamMsg(m.t("files.none"))
		}
	}
	totalEdits := 0
	for _, f := range files {
		totalEdits += f.Edits
	}
	var b strings.Builder
	b.WriteString(m.t("files.title", len(files), totalEdits))
	for _, f := range files {
		newFlag := ""
		if f.IsNew {
			newFlag = " (new)"
		}
		b.WriteString(m.t("files.item", f.Path, f.Edits, f.LastTool, newFlag))
	}
	b.WriteString(m.t("files.hint"))
	return func() tea.Msg {
		return streamMsg(b.String())
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

	// Truncate very large diffs. When truncating, prepend a --stat summary
	// so the user can see which files changed even if the full diff is cut off.
	maxLines := 200
	lines := strings.Split(result, "\n")
	if len(lines) > maxLines {
		// Get the stat summary for context
		statArgs := []string{"diff", "--stat"}
		// Preserve user-specified flags like --cached
		for _, p := range parts[1:] {
			if strings.HasPrefix(p, "--") {
				statArgs = append(statArgs, p)
			}
		}
		statCmd := exec.Command("git", statArgs...)
		statCmd.Dir = workingDirFromModel(m)
		statOutput, statErr := statCmd.CombinedOutput()

		var sb strings.Builder
		if statErr == nil && strings.TrimSpace(string(statOutput)) != "" {
			sb.WriteString(strings.TrimSpace(string(statOutput)))
			sb.WriteString("\n\n")
		}
		sb.WriteString(strings.Join(lines[:maxLines], "\n"))
		sb.WriteString(fmt.Sprintf("\n\n... (%d more lines, run /diff <file> to narrow)", len(lines)-maxLines))
		result = sb.String()
	}

	m.chatWriteSystem(nextSystemID(), "```\n"+result+"\n```")
	return nil
}

// handleHooksCommand opens the hooks configuration panel.
func (m *Model) handleHooksCommand() tea.Cmd {
	m.openHooksPanel()
	return nil
}

// handleCostCommand displays the session token usage and estimated cost,
// grouped by model. A session may use multiple models if the user switches
// mid-session; each model's contribution is shown separately.
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

	// Group UsageHistory by vendor+model for per-model breakdown.
	type modelGroup struct {
		model    string
		vendor   string
		endpoint string
		usage    provider.TokenUsage
	}
	groups := map[string]*modelGroup{}
	var groupOrder []string

	for _, entry := range m.session.UsageHistory {
		key := entry.Vendor + "/" + entry.Model
		g, ok := groups[key]
		if !ok {
			g = &modelGroup{
				model:    entry.Model,
				vendor:   entry.Vendor,
				endpoint: entry.Endpoint,
			}
			groups[key] = g
			groupOrder = append(groupOrder, key)
		}
		g.usage = g.usage.Add(entry.Usage)
	}

	// Fallback: if no UsageHistory entries, use the session-level aggregate.
	if len(groups) == 0 {
		groups[""] = &modelGroup{
			model:  m.session.Model,
			vendor: m.session.Vendor,
			usage:  usage,
		}
		groupOrder = []string{""}
	}

	var sb strings.Builder
	sb.WriteString("Session Cost Breakdown:\n\n")

	var grandCost float64
	var hasMeteredRate bool

	for _, key := range groupOrder {
		g := groups[key]
		gu := g.usage
		if gu.Total() == 0 {
			continue
		}

		// Model header
		sb.WriteString(fmt.Sprintf("  %s (%s)\n", g.model, g.vendor))
		sb.WriteString(fmt.Sprintf("    Input tokens:       %s\n", humanizeTokenCount(gu.InputTokens)))
		sb.WriteString(fmt.Sprintf("    Output tokens:      %s\n", humanizeTokenCount(gu.OutputTokens)))
		if gu.CacheRead > 0 {
			sb.WriteString(fmt.Sprintf("    Cache read:         %s\n", humanizeTokenCount(gu.CacheRead)))
		}
		if gu.CacheWrite > 0 {
			sb.WriteString(fmt.Sprintf("    Cache write:        %s\n", humanizeTokenCount(gu.CacheWrite)))
		}

		// Cost estimate for this model
		rate := resolveRate(g.vendor, g.endpoint, g.model)
		if rate.IsKnown() {
			if !rate.IsMetered() {
				planLabel := rate.Plan
				if planLabel == "" {
					planLabel = string(rate.Type)
				}
				sb.WriteString(fmt.Sprintf("    Cost:               included in %s\n\n", planLabel))
			} else {
				hasMeteredRate = true
				modelCost := float64(gu.InputTokens)*rate.InputPerM/1e6 +
					float64(gu.OutputTokens)*rate.OutputPerM/1e6 +
					float64(gu.CacheRead)*rate.CacheReadPerM/1e6 +
					float64(gu.CacheWrite)*rate.CacheWritePerM/1e6
				grandCost += modelCost
				sb.WriteString(fmt.Sprintf("    Cost:               $%.4f\n", modelCost))
				sb.WriteString(fmt.Sprintf("    (rate: $%.2f/M in, $%.2f/M out)\n\n", rate.InputPerM, rate.OutputPerM))
			}
		} else {
			sb.WriteString("    Cost:               (no pricing data)\n\n")
		}
	}

	// Grand totals
	sb.WriteString("  \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\n")
	sb.WriteString(fmt.Sprintf("  Total tokens:       %s\n", humanizeTokenCount(usage.Total())))
	if hasMeteredRate && grandCost > 0 {
		sb.WriteString(fmt.Sprintf("  Total estimated:    $%.4f\n", grandCost))
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
	if m.agent == nil {
		m.chatWriteSystem(nextSystemID(), "Agent not initialized.")
		return nil
	}

	cm := m.agent.ContextManager()
	if cm == nil {
		m.chatWriteSystem(nextSystemID(), "Context manager not available.")
		return nil
	}

	ctxWindow := cm.ContextWindow()
	if ctxWindow <= 0 {
		m.chatWriteSystem(nextSystemID(), "Context window size unknown.")
		return nil
	}

	messages := cm.Messages()
	totalTokens := cm.TokenCount()
	usageRatio := cm.UsageRatio()
	compactThreshold := cm.AutoCompactThreshold()

	// System prompt token estimate
	sysPrompt := m.agent.SystemPrompt()
	sysTokens := context.EstimateTokens(sysPrompt)

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

	m.chatWriteSystem(nextSystemID(), sb.String())
	return nil
}

// estimateMessageTokens sums token estimates across all content blocks.
// Uses context.EstimateTokens for text (ASCII fast path + CJK aware),
// and adds overhead for images and tool calls.
func estimateMessageTokens(msg provider.Message) int {
	var total int
	for _, block := range msg.Content {
		switch block.Type {
		case "text":
			total += context.EstimateTokens(block.Text)
		case "image":
			// Vision tokens are larger; approximate as 1000 per image
			total += 1000
		default:
			// Tool calls/results have JSON structure overhead
			total += context.EstimateTokens(block.Output+block.ToolName+string(block.Input)) + 6
		}
	}
	return total
}

// handleRegenerateCommand discards the agent's most recent response and
// re-runs the agent with the last user message. This is equivalent to
// ChatGPT's "Regenerate" button. It removes the last assistant message
// (and trailing tool messages) from the context manager, updates the chat
// display, and starts a new agent run without adding a new user message.
func (m *Model) handleRegenerateCommand() tea.Cmd {
	if m.loading {
		m.chatWriteSystem(nextSystemID(), m.t("regenerate.busy"))
		return nil
	}
	if m.agent == nil {
		m.chatWriteSystem(nextSystemID(), m.t("regenerate.no_agent"))
		return nil
	}
	cm := m.agent.ContextManager()
	if cm == nil {
		m.chatWriteSystem(nextSystemID(), m.t("regenerate.no_context"))
		return nil
	}

	// Remove the last assistant group from the context manager.
	lastUserText := cm.RemoveLastAssistantGroup()
	if lastUserText == "" {
		m.chatWriteSystem(nextSystemID(), m.t("regenerate.no_response"))
		return nil
	}

	// Remove the agent's response from the chat display.
	if m.chatList != nil {
		m.chatList.TruncateAfterLastUser()
	}

	// Also remove the last assistant message from the session's message list
	// so that persistFullSessionMessages doesn't write stale data.
	m.sessionMutex().Lock()
	if m.session != nil && len(m.session.Messages) > 0 {
		// Find last user message index in session.
		lastUserIdx := -1
		for i := len(m.session.Messages) - 1; i >= 0; i-- {
			if m.session.Messages[i].Role == "user" {
				lastUserIdx = i
				break
			}
		}
		if lastUserIdx >= 0 {
			m.session.Messages = m.session.Messages[:lastUserIdx+1]
		}
	}
	m.sessionMutex().Unlock()

	// Start a new agent run with the existing user message text.
	// We do NOT call appendUserMessage because the user message is
	// already in the context manager from the original submission.
	m.streamBuffer = nil
	m.shellBuffer = nil
	m.streamPrefixWritten = false
	m.setLoading(true)
	m.loopStart = time.Now()
	m.statusActivity = m.t("status.thinking")
	m.statusToolName = ""
	m.statusToolArg = ""
	m.statusToolCount = 0

	// Reset run tracking so persistFullSessionMessages only writes
	// new messages from this run, not the existing context.
	if m.agent != nil {
		m.agent.StartRunTracking()
	}

	return tea.Batch(m.startLoadingSpinner(m.statusActivity), m.startAgentWithExpand(lastUserText))
}
