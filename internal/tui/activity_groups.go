package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/chat"
	"github.com/topcheer/ggcode/internal/subagent"
)

// chatFinishAllRunningTools marks all running tool items in chatList as success.
// Called when a round ends (doneMsg) to finalize any tool items that weren't
// explicitly finished via chatFinishTool.
func (m *Model) chatFinishAllRunningTools() {
	if m.chatList == nil {
		return
	}
	type statusAccessor interface {
		Status() chat.ToolStatus
		SetStatus(chat.ToolStatus)
	}
	for i := 0; i < m.chatList.Len(); i++ {
		item := m.chatList.ItemAt(i)
		if item == nil {
			continue
		}
		if sa, ok := item.(statusAccessor); ok && sa.Status() == chat.StatusRunning {
			sa.SetStatus(chat.StatusSuccess)
		}
	}
}

// isLiveSubAgentStatus returns true for subagent statuses that indicate active work.
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

func isCommandTool(toolName string) bool {
	switch toolName {
	case "run_command", "bash", "powershell", "start_command", "write_command_input":
		return true
	default:
		return false
	}
}

func trimLeadingRenderedSpacing(rendered string) string {
	return strings.TrimLeft(rendered, "\n\r ")
}

type todoStateItem struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	Status    string    `json:"status"`
	StartedAt time.Time `json:"-"`
	UpdatedAt time.Time `json:"-"`
}

func (m *Model) applyTodoWrite(ts ToolStatusMsg) string {
	todos, ok := parseTodoSnapshot(ts.RawArgs)
	if !ok {
		// Not a valid todo_write — return a generic summary.
		present := describeTool(m.currentLanguage(), ts.ToolName, ts.RawArgs)
		return formatToolInline(present.DisplayName, present.Detail)
	}

	previous := m.todoSnapshot
	if previous == nil {
		previous = map[string]todoStateItem{}
	}
	current := make(map[string]todoStateItem, len(todos))
	changes := make([]string, 0, len(todos))
	now := time.Now()

	for _, td := range todos {
		if prev, existed := previous[td.ID]; existed {
			td.StartedAt = prev.StartedAt
			td.UpdatedAt = prev.UpdatedAt
		}
		if td.UpdatedAt.IsZero() {
			td.UpdatedAt = now
		}
		switch td.Status {
		case "in_progress":
			if td.StartedAt.IsZero() {
				td.StartedAt = now
			}
			td.UpdatedAt = now
		case "done", "blocked", "failed":
			if td.StartedAt.IsZero() {
				td.StartedAt = now
			}
		}
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
	m.todoOrder = make([]string, 0, len(todos))
	for _, td := range todos {
		m.todoOrder = append(m.todoOrder, td.ID)
	}
	m.activeTodo = nil
	// Auto-show sidebar when task mode starts (first todo_write with active items).
	// Auto-hide when all todos reach terminal state (done/failed/blocked) or are cleared.
	if len(previous) == 0 && len(current) > 0 {
		// Task mode just started — ensure sidebar is visible.
		m.sidebarVisible = true
	} else if len(current) > 0 {
		allDone := true
		for _, td := range current {
			if td.Status != "done" && td.Status != "failed" && td.Status != "blocked" {
				allDone = false
				break
			}
		}
		if allDone {
			m.sidebarVisible = false
		}
	}
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
	// Update TodoToolItem in chatList
	m.chatUpdateTodoItem(todos)

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
