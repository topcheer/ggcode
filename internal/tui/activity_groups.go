package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

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
	group.Items = append(group.Items, toolActivityItem{
		Summary: formatToolInline(toolDisplayName(ts), toolDetail(ts)),
		Running: true,
	})
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
		item.Running = false
		if m.activeTodo != nil {
			group.TodoID = m.activeTodo.ID
			group.TodoContent = m.activeTodo.Content
		}
		group.Title = localizeToolGroupTitle(m.currentLanguage(), group.Categories)
		m.closeToolActivityGroup()
		return
	}
	item.Summary = formatToolItemSummary(m.currentLanguage(), ts)
	item.Running = false
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
	var rows []string
	lastTodoID := ""
	for _, group := range m.activityGroups {
		if group.Title == "" || len(group.Items) == 0 {
			continue
		}
		if group.TodoContent != "" && group.TodoID != lastTodoID {
			rows = append(rows, fmt.Sprintf(" 🎯 %s", localizeTodoHeading(m.currentLanguage(), group.TodoContent)))
			lastTodoID = group.TodoID
		}
		rows = append(rows, fmt.Sprintf(" 📦 %s", group.Title))
		items, hiddenCount := visibleGroupItems(group.Items)
		if hiddenCount > 0 {
			rows = append(rows, fmt.Sprintf("    %s", localizeHiddenStepsSummary(m.currentLanguage(), hiddenCount)))
		}
		for _, item := range items {
			prefix := "•"
			if item.Running {
				prefix = "◦"
			}
			rows = append(rows, fmt.Sprintf("    %s %s", prefix, truncateString(item.Summary, m.conversationInnerWidth()-10)))
		}
	}
	return strings.Join(rows, "\n")
}

func (m Model) renderLiveActivities() string {
	parts := make([]string, 0, 2)
	if groups := m.renderGroupedActivities(); groups != "" {
		parts = append(parts, groups)
	}
	if agents := m.renderSubAgentActivities(); agents != "" {
		parts = append(parts, agents)
	}
	return strings.Join(parts, "\n")
}

func (m Model) renderSubAgentActivities() string {
	if m.subAgentMgr == nil {
		return ""
	}
	agents := m.subAgentMgr.List()
	if len(agents) == 0 {
		return ""
	}
	sort.SliceStable(agents, func(i, j int) bool {
		return agents[i].CreatedAt.Before(agents[j].CreatedAt)
	})

	var rows []string
	for _, sa := range agents {
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

func (m Model) subAgentActivitySummary(sa *subagent.SubAgent) string {
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
	summary := summarizeToolResult(lang, msg)
	if summary == "" {
		return formatToolInline(toolDisplayName(msg), toolDetail(msg))
	}
	return fmt.Sprintf("%s — %s", formatToolInline(toolDisplayName(msg), toolDetail(msg)), summary)
}

func classifyToolGroup(toolName string) string {
	switch toolName {
	case "read_file", "glob", "grep", "search_files", "list_directory", "git_diff", "git_status", "git_log":
		return "explore"
	case "edit_file", "write_file":
		return "edit"
	case "todo_write":
		return "todo"
	case "run_command", "bash", "powershell":
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
