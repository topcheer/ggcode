package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/topcheer/ggcode/internal/subagent"
)

const maxActivityGroups = 4
const maxVisibleGroupItems = 5

func (m *Model) startToolActivity(ts ToolStatusMsg) {
	if isSubAgentLifecycleTool(ts.ToolName) {
		return
	}
	if ts.ToolName == "todo_write" && len(m.activityGroups) > 0 && m.activityGroups[len(m.activityGroups)-1].Active && len(m.activityGroups[len(m.activityGroups)-1].Items) > 0 {
		m.closeToolActivityGroup()
	}
	if len(m.activityGroups) == 0 || !m.activityGroups[len(m.activityGroups)-1].Active {
		group := toolActivityGroup{Active: true}
		if m.activeTodo != nil {
			group.TodoID = m.activeTodo.ID
			group.TodoContent = m.activeTodo.Content
		}
		m.activityGroups = append(m.activityGroups, group)
		if len(m.activityGroups) > maxActivityGroups {
			m.activityGroups = m.activityGroups[len(m.activityGroups)-maxActivityGroups:]
		}
	}
	group := &m.activityGroups[len(m.activityGroups)-1]
	kind := classifyToolGroup(ts.ToolName)
	if !containsString(group.Categories, kind) {
		group.Categories = append(group.Categories, kind)
	}
	group.Title = localizeToolGroupTitle(m.currentLanguage(), group.Categories)
	group.Items = append(group.Items, buildToolActivityItem(m.currentLanguage(), ts))
}

func (m *Model) finishToolActivity(ts ToolStatusMsg) {
	if isSubAgentLifecycleTool(ts.ToolName) {
		return
	}
	if len(m.activityGroups) == 0 {
		return
	}
	group := &m.activityGroups[len(m.activityGroups)-1]
	if len(group.Items) == 0 {
		return
	}
	item := &group.Items[len(group.Items)-1]
	if ts.ToolName == "todo_write" {
		item.Summary = m.applyTodoWrite(ts)
		item.CommandTitle = ""
		item.CommandLines = nil
		item.CommandHiddenLineCount = 0
		item.OutputLines = nil
		item.OutputHiddenLineCount = 0
		item.Running = false
		if m.activeTodo != nil {
			group.TodoID = m.activeTodo.ID
			group.TodoContent = m.activeTodo.Content
		}
		group.Title = localizeToolGroupTitle(m.currentLanguage(), group.Categories)
		m.closeToolActivityGroup()
		return
	}
	*item = buildToolActivityItem(m.currentLanguage(), ts)
}

func (m *Model) closeToolActivityGroup() {
	if len(m.activityGroups) == 0 {
		return
	}
	m.activityGroups[len(m.activityGroups)-1].Active = false
}

func (m *Model) resetActivityGroups() {
	m.activityGroups = nil
}

func (m *Model) flushGroupedActivitiesToOutput() {
	grouped := m.renderGroupedActivities()
	if grouped == "" {
		return
	}
	if m.output.Len() > 0 && !strings.HasSuffix(m.output.String(), "\n") {
		m.output.WriteString("\n")
	}
	m.output.WriteString(grouped)
	m.output.WriteString("\n")
	m.resetActivityGroups()
}

func (m Model) renderGroupedActivities() string {
	type renderedSection struct {
		Title string
		Items []toolActivityItem
	}
	type renderedBlock struct {
		TodoID   string
		Sections []renderedSection
	}

	var blocks []renderedBlock
	for _, group := range m.activityGroups {
		if group.Title == "" || len(group.Items) == 0 {
			continue
		}
		if len(blocks) == 0 || blocks[len(blocks)-1].TodoID != group.TodoID {
			blocks = append(blocks, renderedBlock{TodoID: group.TodoID})
		}
		block := &blocks[len(blocks)-1]
		if len(block.Sections) > 0 && block.Sections[len(block.Sections)-1].Title == group.Title {
			block.Sections[len(block.Sections)-1].Items = append(block.Sections[len(block.Sections)-1].Items, group.Items...)
			continue
		}
		block.Sections = append(block.Sections, renderedSection{Title: group.Title, Items: group.Items})
	}

	var renderedBlocks []string
	for _, block := range blocks {
		var sections []string
		for _, section := range block.Sections {
			rows := []string{fmt.Sprintf(" 📦 %s", section.Title)}
			items, hiddenCount := visibleGroupItems(section.Items)
			if hiddenCount > 0 {
				rows = append(rows, fmt.Sprintf("    %s", localizeHiddenStepsSummary(m.currentLanguage(), hiddenCount)))
			}
			for _, item := range items {
				rows = append(rows, m.renderToolActivityItem(item)...)
			}
			sections = append(sections, strings.Join(rows, "\n"))
		}
		if len(sections) > 0 {
			renderedBlocks = append(renderedBlocks, strings.Join(sections, "\n\n"))
		}
	}

	return strings.Join(renderedBlocks, "\n\n")
}

func (m Model) renderLiveActivities() string {
	parts := make([]string, 0, 2)
	if groups := m.renderGroupedActivities(); groups != "" {
		parts = append(parts, groups)
	}
	if agents := m.renderSubAgentActivities(); agents != "" {
		parts = append(parts, agents)
	}
	return strings.Join(parts, "\n\n")
}

func (m Model) renderSubAgentActivities() string {
	if m.subAgentMgr == nil {
		return ""
	}
	agents := m.subAgentMgr.List()
	liveAgents := make([]*subagent.SubAgent, 0, len(agents))
	for _, sa := range agents {
		if isLiveSubAgentStatus(sa.Status) {
			liveAgents = append(liveAgents, sa)
		}
	}
	if len(liveAgents) == 0 {
		return ""
	}
	sort.SliceStable(liveAgents, func(i, j int) bool {
		return liveAgents[i].CreatedAt.Before(liveAgents[j].CreatedAt)
	})

	var rows []string
	for _, sa := range liveAgents {
		task := truncateString(compactSingleLine(firstNonEmpty(sa.DisplayTask, sa.Task)), 54)
		if task == "" {
			task = sa.ID
		}
		statusIcon := "⏳"
		switch sa.Status {
		case subagent.StatusCompleted:
			statusIcon = "✅"
		case subagent.StatusFailed, subagent.StatusCancelled:
			statusIcon = "✕"
		case subagent.StatusPending:
			statusIcon = "…"
		}
		rows = append(rows, fmt.Sprintf(" 🤖 %s %s", statusIcon, task))
		rows = append(rows, fmt.Sprintf("    %s • %d tools", localizeSubAgentStatus(m.currentLanguage(), sa.Status), sa.ToolCallCount))
		rows = append(rows, fmt.Sprintf("    %s", truncateString(m.subAgentActivitySummary(sa), m.conversationInnerWidth()-8)))
	}
	return strings.Join(rows, "\n")
}

func isLiveSubAgentStatus(status subagent.Status) bool {
	switch status {
	case subagent.StatusPending, subagent.StatusRunning:
		return true
	default:
		return false
	}
}

func (m Model) subAgentActivitySummary(sa *subagent.SubAgent) string {
	if summary := strings.TrimSpace(sa.ProgressSummary); summary != "" {
		return summary
	}
	if sa.CurrentTool != "" {
		present := describeTool(m.currentLanguage(), sa.CurrentTool, sa.CurrentArgs)
		return firstNonEmpty(present.Activity, formatToolInline(present.DisplayName, present.Detail))
	}
	switch sa.CurrentPhase {
	case "writing":
		return m.t("status.writing")
	case "thinking":
		return m.t("status.thinking")
	case "completed":
		if m.currentLanguage() == LangZhCN {
			return "已完成"
		}
		return "Completed"
	case "failed":
		if m.currentLanguage() == LangZhCN {
			return "已失败"
		}
		return "Failed"
	case "pending":
		if m.currentLanguage() == LangZhCN {
			return "等待开始"
		}
		return "Pending"
	default:
		return m.t("status.thinking")
	}
}

func formatToolItemSummary(lang Language, msg ToolStatusMsg) string {
	if summary, ok := formatCommandToolItemSummary(lang, msg); ok {
		return summary
	}
	summary := summarizeToolResult(lang, msg)
	if isTrivialToolDetail(summary) || strings.EqualFold(summary, toolDisplayName(msg)) {
		summary = ""
	}
	if summary == "" {
		return formatToolInline(toolDisplayName(msg), toolDetail(msg))
	}
	return fmt.Sprintf("%s — %s", formatToolInline(toolDisplayName(msg), toolDetail(msg)), summary)
}

func formatCommandToolItemSummary(lang Language, msg ToolStatusMsg) (string, bool) {
	if !isCommandTool(msg.ToolName) {
		return "", false
	}
	preview := buildCommandPreview(rawCommandArg(parseToolArgs(msg.RawArgs)))
	if preview.Title == "" && len(preview.CommandLines) == 0 {
		return "", false
	}

	title := preview.Title
	if title == "" && len(preview.CommandLines) > 0 {
		title = preview.CommandLines[0]
		preview.CommandLines = preview.CommandLines[1:]
	}
	lines := make([]string, 0, 2+len(preview.CommandLines))
	lines = append(lines, title)
	lines = append(lines, preview.CommandLines...)
	if preview.CommandHiddenLineCount > 0 {
		lines = append(lines, localizeMoreLinesSummary(lang, preview.CommandHiddenLineCount))
	}
	if summary := summarizeToolResult(lang, msg); summary != "" {
		lines = append(lines, summary)
	}
	return strings.Join(lines, "\n"), true
}

func buildToolActivityItem(lang Language, msg ToolStatusMsg) toolActivityItem {
	if item, ok := buildCommandToolActivityItem(lang, msg); ok {
		return item
	}
	return toolActivityItem{
		Summary: formatToolItemSummary(lang, msg),
		Running: msg.Running,
	}
}

func buildCommandToolActivityItem(_ Language, msg ToolStatusMsg) (toolActivityItem, bool) {
	if !isCommandTool(msg.ToolName) {
		return toolActivityItem{}, false
	}
	preview := buildCommandPreview(rawCommandArg(parseToolArgs(msg.RawArgs)))
	outputPreview := buildTextPreview(msg.Result)

	item := toolActivityItem{
		Running:                msg.Running,
		CommandTitle:           preview.Title,
		CommandLines:           preview.CommandLines,
		CommandHiddenLineCount: preview.CommandHiddenLineCount,
		OutputLines:            outputPreview.Lines,
		OutputHiddenLineCount:  outputPreview.HiddenLineCount,
	}
	if item.CommandTitle == "" && len(item.CommandLines) == 0 {
		item.Summary = formatToolInline(toolDisplayName(msg), toolDetail(msg))
	}
	return item, true
}

func classifyToolGroup(toolName string) string {
	switch toolName {
	case "read_file", "glob", "grep", "search_files", "list_directory", "git_diff", "git_status", "git_log":
		return "explore"
	case "edit_file", "write_file":
		return "edit"
	case "todo_write":
		return "todo"
	case "run_command", "bash", "powershell", "start_command", "write_command_input", "read_command_output", "wait_command", "stop_command", "list_commands":
		return "run"
	case "web_fetch", "web_search":
		return "research"
	default:
		return "general"
	}
}

func localizeToolGroupTitle(lang Language, categories []string) string {
	if len(categories) == 0 {
		if lang == LangZhCN {
			return "工具活动"
		}
		return "Tool activity"
	}
	if len(categories) == 1 {
		switch categories[0] {
		case "explore":
			if lang == LangZhCN {
				return "浏览项目上下文"
			}
			return "Exploring project context"
		case "edit":
			if lang == LangZhCN {
				return "修改文件"
			}
			return "Updating files"
		case "run":
			if lang == LangZhCN {
				return "执行命令"
			}
			return "Running commands"
		case "todo":
			if lang == LangZhCN {
				return "推进任务"
			}
			return "Advancing tasks"
		case "research":
			if lang == LangZhCN {
				return "检索外部信息"
			}
			return "Researching"
		}
	}
	if lang == LangZhCN {
		return "协调工具调用"
	}
	return "Using tools"
}

func localizeSubAgentStatus(lang Language, status subagent.Status) string {
	switch status {
	case subagent.StatusPending:
		if lang == LangZhCN {
			return "等待中"
		}
		return "Pending"
	case subagent.StatusRunning:
		if lang == LangZhCN {
			return "运行中"
		}
		return "Running"
	case subagent.StatusCompleted:
		if lang == LangZhCN {
			return "已完成"
		}
		return "Completed"
	case subagent.StatusFailed:
		if lang == LangZhCN {
			return "失败"
		}
		return "Failed"
	case subagent.StatusCancelled:
		if lang == LangZhCN {
			return "已取消"
		}
		return "Cancelled"
	default:
		return string(status)
	}
}

func containsString(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}

func isCommandTool(toolName string) bool {
	switch toolName {
	case "run_command", "bash", "powershell", "start_command", "write_command_input":
		return true
	default:
		return false
	}
}

func visibleGroupItems(items []toolActivityItem) ([]toolActivityItem, int) {
	if len(items) <= maxVisibleGroupItems {
		return items, 0
	}
	hidden := len(items) - maxVisibleGroupItems
	return items[hidden:], hidden
}

func localizeHiddenStepsSummary(lang Language, hidden int) string {
	if lang == LangZhCN {
		return fmt.Sprintf("… 前面还有 %d 条已完成步骤", hidden)
	}
	if hidden == 1 {
		return "… 1 earlier completed step"
	}
	return fmt.Sprintf("… %d earlier completed steps", hidden)
}

func localizeMoreLinesSummary(lang Language, hidden int) string {
	if lang == LangZhCN {
		return fmt.Sprintf("… 还有 %d 行脚本", hidden)
	}
	if hidden == 1 {
		return "… 1 more script line"
	}
	return fmt.Sprintf("… %d more script lines", hidden)
}

func (m Model) renderToolActivityItem(item toolActivityItem) []string {
	prefix := "•"
	if item.Running {
		prefix = "◦"
	}
	if item.CommandTitle != "" || len(item.CommandLines) > 0 || len(item.OutputLines) > 0 {
		return m.renderCommandActivityItem(item, prefix)
	}
	lines := strings.Split(item.Summary, "\n")
	if len(lines) == 0 {
		return nil
	}

	firstWidth := m.conversationInnerWidth() - 10
	bodyWidth := m.conversationInnerWidth() - 8
	if firstWidth < 8 {
		firstWidth = 8
	}
	if bodyWidth < 8 {
		bodyWidth = 8
	}
	rows := []string{fmt.Sprintf("    %s %s", toolBulletStyle.Render(prefix), truncateString(lines[0], firstWidth))}
	for _, line := range lines[1:] {
		rows = append(rows, fmt.Sprintf("      %s", truncateString(line, bodyWidth)))
	}
	return rows
}

func (m Model) renderCommandActivityItem(item toolActivityItem, prefix string) []string {
	firstWidth := m.conversationInnerWidth() - 10
	bodyWidth := m.conversationInnerWidth() - 8
	if firstWidth < 8 {
		firstWidth = 8
	}
	if bodyWidth < 8 {
		bodyWidth = 8
	}

	commandStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	resultStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("45"))
	rows := make([]string, 0, 1+len(item.CommandLines)+len(item.OutputLines))

	commandLines := append([]string(nil), item.CommandLines...)
	if item.CommandTitle == "" && len(commandLines) > 0 {
		first := appendHiddenLineSuffix(m.currentLanguage(), commandLines[0], item.CommandHiddenLineCount, "command")
		rows = append(rows, fmt.Sprintf("    %s %s", toolBulletStyle.Render(prefix), commandStyle.Render(truncateString(first, firstWidth))))
		commandLines = commandLines[1:]
	} else {
		header := item.CommandTitle
		if header == "" {
			header = item.Summary
		}
		rows = append(rows, fmt.Sprintf("    %s %s", toolBulletStyle.Render(prefix), truncateString(header, firstWidth)))
		if item.CommandHiddenLineCount > 0 && len(commandLines) == 0 {
			rows[len(rows)-1] = fmt.Sprintf("    %s %s", toolBulletStyle.Render(prefix), truncateString(appendHiddenLineSuffix(m.currentLanguage(), header, item.CommandHiddenLineCount, "command"), firstWidth))
		}
	}

	for i, line := range commandLines {
		if i == len(commandLines)-1 {
			line = appendHiddenLineSuffix(m.currentLanguage(), line, item.CommandHiddenLineCount, "command")
		}
		rows = append(rows, fmt.Sprintf("      %s", commandStyle.Render(truncateString(line, bodyWidth))))
	}

	for i, line := range item.OutputLines {
		if i == len(item.OutputLines)-1 {
			line = appendHiddenLineSuffix(m.currentLanguage(), line, item.OutputHiddenLineCount, "output")
		}
		rows = append(rows, fmt.Sprintf("      %s", resultStyle.Render(truncateString(line, bodyWidth))))
	}

	return rows
}

func appendHiddenLineSuffix(lang Language, line string, hidden int, kind string) string {
	if hidden <= 0 {
		return line
	}
	suffix := ""
	switch kind {
	case "command":
		if lang == LangZhCN {
			suffix = fmt.Sprintf(" … 还有 %d 行脚本", hidden)
		} else if hidden == 1 {
			suffix = " … 1 more script line"
		} else {
			suffix = fmt.Sprintf(" … %d more script lines", hidden)
		}
	default:
		if lang == LangZhCN {
			suffix = fmt.Sprintf(" … 还有 %d 行输出", hidden)
		} else if hidden == 1 {
			suffix = " … 1 more output line"
		} else {
			suffix = fmt.Sprintf(" … %d more output lines", hidden)
		}
	}
	if strings.TrimSpace(line) == "" {
		return strings.TrimSpace(suffix)
	}
	return line + suffix
}

func trimLeadingRenderedSpacing(rendered string) string {
	return strings.TrimLeft(rendered, "\n\r ")
}

type todoStateItem struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Status  string `json:"status"`
}

func (m *Model) applyTodoWrite(ts ToolStatusMsg) string {
	todos, ok := parseTodoSnapshot(ts.RawArgs)
	if !ok {
		return formatToolItemSummary(m.currentLanguage(), ts)
	}

	previous := m.todoSnapshot
	if previous == nil {
		previous = map[string]todoStateItem{}
	}
	current := make(map[string]todoStateItem, len(todos))
	changes := make([]string, 0, len(todos))

	for _, td := range todos {
		current[td.ID] = td
		prev, existed := previous[td.ID]
		switch {
		case !existed && td.Status == "in_progress":
			changes = append(changes, localizeTodoChange(m.currentLanguage(), "started", td))
		case !existed && td.Status == "done":
			changes = append(changes, localizeTodoChange(m.currentLanguage(), "completed", td))
		case !existed:
			changes = append(changes, localizeTodoChange(m.currentLanguage(), "added", td))
		case prev.Status != td.Status && td.Status == "in_progress":
			changes = append(changes, localizeTodoChange(m.currentLanguage(), "started", td))
		case prev.Status != td.Status && td.Status == "done":
			changes = append(changes, localizeTodoChange(m.currentLanguage(), "completed", td))
		case prev.Status != td.Status:
			changes = append(changes, localizeTodoChange(m.currentLanguage(), "updated", td))
		}
	}
	for id, prev := range previous {
		if _, exists := current[id]; !exists {
			changes = append(changes, localizeTodoChange(m.currentLanguage(), "removed", prev))
		}
	}

	m.todoSnapshot = current
	m.activeTodo = nil
	for _, td := range todos {
		if td.Status == "in_progress" {
			tdCopy := td
			m.activeTodo = &tdCopy
			break
		}
	}

	if len(changes) == 0 {
		if m.activeTodo != nil {
			return localizeTodoFocus(m.currentLanguage(), m.activeTodo.Content)
		}
		if m.currentLanguage() == LangZhCN {
			return "同步待办状态"
		}
		return "Synced todo state"
	}
	return summarizeTodoChanges(m.currentLanguage(), changes)
}

func parseTodoSnapshot(rawArgs string) ([]todoStateItem, bool) {
	if strings.TrimSpace(rawArgs) == "" {
		return nil, false
	}
	var args struct {
		Todos []todoStateItem `json:"todos"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil || len(args.Todos) == 0 {
		return nil, false
	}
	return args.Todos, true
}

func summarizeTodoChanges(lang Language, changes []string) string {
	if len(changes) == 0 {
		return ""
	}
	if len(changes) == 1 {
		return changes[0]
	}
	visible := changes
	if len(visible) > 2 {
		visible = visible[:2]
	}
	summary := strings.Join(visible, "; ")
	if remaining := len(changes) - len(visible); remaining > 0 {
		if lang == LangZhCN {
			summary += fmt.Sprintf("；另有 %d 项", remaining)
		} else {
			summary += fmt.Sprintf("; %d more", remaining)
		}
	}
	return summary
}

func localizeTodoChange(lang Language, kind string, td todoStateItem) string {
	label := truncateString(compactSingleLine(td.Content), 48)
	if label == "" {
		label = td.ID
	}
	switch lang {
	case LangZhCN:
		switch kind {
		case "started":
			return "开始任务 " + label
		case "completed":
			return "完成任务 " + label
		case "added":
			return "新增待办 " + label
		case "removed":
			return "移除待办 " + label
		default:
			return "更新待办 " + label
		}
	default:
		switch kind {
		case "started":
			return "Started " + label
		case "completed":
			return "Completed " + label
		case "added":
			return "Added " + label
		case "removed":
			return "Removed " + label
		default:
			return "Updated " + label
		}
	}
}

func localizeTodoHeading(lang Language, content string) string {
	content = truncateString(compactSingleLine(content), 72)
	if lang == LangZhCN {
		return "任务: " + content
	}
	return "Todo: " + content
}

func localizeTodoFocus(lang Language, content string) string {
	content = truncateString(compactSingleLine(content), 60)
	if lang == LangZhCN {
		return "当前任务 " + content
	}
	return "Working on " + content
}

func isSubAgentLifecycleTool(toolName string) bool {
	switch toolName {
	case "spawn_agent", "wait_agent", "list_agents":
		return true
	default:
		return false
	}
}
