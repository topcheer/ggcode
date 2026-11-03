package tui

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/topcheer/ggcode/internal/chat"
	"github.com/topcheer/ggcode/internal/tool"
)

func (m *Model) chatWrite(item chat.Item) {
	if m.chatList != nil {
		m.chatList.Append(item)
	}
}

func (m *Model) chatWriteUser(id, text string) {
	m.chatWrite(chat.NewUserItem(id, text, m.chatStyles))
}

func (m *Model) chatUpdateToolOutput(toolID, output string) {
	if m.chatList == nil || toolID == "" {
		return
	}
	m.chatList.UpdateByID(toolID, func(item chat.Item) {
		type streamSetter interface {
			SetStreamingBody(string)
		}
		if ss, ok := item.(streamSetter); ok {
			ss.SetStreamingBody(output)
		}
	})
}

func (m *Model) chatWriteUserMarkdown(id, text string) {
	m.chatWrite(chat.NewMarkdownUserItem(id, text, m.chatStyles))
}

func (m *Model) chatWriteSystem(id, text string) {
	m.chatWrite(chat.NewSystemItem(id, text, m.chatStyles))
	if m.suppressNextTunnelSystem == text {
		m.suppressNextTunnelSystem = ""
		return
	}
	if broker := m.tunnelEventBroker(); broker != nil && strings.TrimSpace(text) != "" {
		broker.PushSystemMessage(text)
	}
}

func (m *Model) chatAppendSystemText(id, text string) {
	if m.chatList == nil {
		return
	}
	if item := m.chatList.FindByID(id); item != nil {
		if s, ok := item.(*chat.SystemItem); ok {
			s.AppendText(text)
		}
	}
}

func (m *Model) chatUpdateAssistantText(id, text string) {
	if m.chatList == nil {
		return
	}
	if item := m.chatList.FindByID(id); item != nil {
		if a, ok := item.(*chat.AssistantItem); ok {
			a.SetText(text)
		}
	}
}

func (m *Model) chatFinishAssistant(id string) {
	if m.chatList == nil {
		return
	}
	if item := m.chatList.FindByID(id); item != nil {
		if a, ok := item.(*chat.AssistantItem); ok {
			a.SetFinished()
		}
	}
}

func (m *Model) chatReset() {
	if m.chatList != nil {
		m.chatList.SetItems(nil)
	}
}

func (m *Model) chatListScrollToBottom() {
	if m.chatList != nil {
		m.chatList.ScrollToEnd()
	}
}

func (m *Model) chatListFollowOutput() {
	if m.chatList != nil {
		m.chatList.FollowOutput()
	}
}

func (m *Model) chatStartTool(ts ToolStatusMsg) {
	if m.chatList == nil {
		return
	}

	if ts.ToolName == "spawn_agent" {
		id := ts.ToolID
		if id == "" {
			id = nextChatID()
		}
		taskDisplay := ts.DisplayName
		if taskDisplay == "" {
			taskDisplay = ts.Detail
		}
		if taskDisplay == "" {
			taskDisplay = ts.Args
		}
		item := chat.NewAgentToolItem(id, taskDisplay, chat.StatusRunning, m.chatStyles)
		m.chatWrite(item)
		return
	}

	if ts.ToolName == "todo_write" {
		return
	}

	switch ts.ToolName {
	case "read_command_output", "stop_command",
		"write_command_input", "list_commands":
		return
	}

	if ts.ToolName == "enter_plan_mode" {
		label := "Planning..."
		var args struct {
			Description string `json:"description"`
		}
		if json.Unmarshal([]byte(ts.RawArgs), &args) == nil && args.Description != "" {
			label = args.Description
		}
		m.emitIMText(label)
		return
	}

	if ts.ToolName == "exit_plan_mode" {
		return
	}

	if strings.HasPrefix(ts.ToolName, "lsp_") {
		return
	}

	id := ts.ToolID
	if id == "" {
		id = nextChatID()
	}
	existing := m.chatList.FindByID(id)
	if existing != nil {

		m.chatUpdateToolStatus(id, chat.StatusRunning)
		return
	}

	displayName := ts.DisplayName
	detail := ts.Detail
	if displayName == "" || detail == "" {
		present := describeTool(m.currentLanguage(), ts.ToolName, ts.RawArgs)
		if displayName == "" {
			displayName = present.DisplayName
		}
		if detail == "" {
			detail = present.Detail
		}
	}

	ctx := chat.ToolContext{
		ToolName:    ts.ToolName,
		DisplayName: displayName,
		Detail:      detail,
		RawArgs:     ts.RawArgs,
		Lang:        string(m.currentLanguage()),
	}
	item := chat.NewToolItem(id, ctx, chat.StatusRunning, m.chatStyles)
	m.chatList.Append(item)
}

func (m *Model) chatFinishTool(ts ToolStatusMsg) {
	if m.chatList == nil {
		return
	}

	if ts.ToolName == "todo_write" {
		m.applyTodoWrite(ts)
		return
	}

	if ts.ToolName == "spawn_agent" {
		id := ts.ToolID
		if id == "" {
			return
		}
		item := m.chatList.FindByID(id)
		if item == nil {
			return
		}
		status := chat.StatusSuccess
		if ts.IsError {
			status = chat.StatusError
		}
		m.chatUpdateToolStatus(id, status)
		m.setToolResult(item, ts.Result, ts.IsError)
		if ts.Elapsed > 0 {
			m.setToolElapsed(item, ts.Elapsed)
		}
		return
	}

	switch ts.ToolName {
	case "read_command_output", "stop_command",
		"write_command_input", "list_commands":
		return
	}

	if strings.HasPrefix(ts.ToolName, "lsp_") {
		return
	}

	if ts.ToolName == "exit_plan_mode" {
		var args struct {
			Plan string `json:"plan"`
		}
		if json.Unmarshal([]byte(ts.RawArgs), &args) == nil && args.Plan != "" {
			item := chat.NewAssistantItem(ts.ToolID, m.chatStyles)
			item.SetText(args.Plan)
			item.SetFinished()
			m.chatList.Append(item)
		}
		return
	}

	id := ts.ToolID
	if id == "" {
		return
	}

	status := chat.StatusSuccess
	if ts.IsError {
		status = chat.StatusError
	}

	existing := m.chatList.FindByID(id)
	if existing == nil {

		displayName := ts.DisplayName
		detail := ts.Detail
		if displayName == "" || detail == "" {
			present := describeTool(m.currentLanguage(), ts.ToolName, ts.RawArgs)
			if displayName == "" {
				displayName = present.DisplayName
			}
			if detail == "" {
				detail = present.Detail
			}
		}
		ctx := chat.ToolContext{
			ToolName:    ts.ToolName,
			DisplayName: displayName,
			Detail:      detail,
			RawArgs:     ts.RawArgs,
			Lang:        string(m.currentLanguage()),
		}
		item := chat.NewToolItem(id, ctx, status, m.chatStyles)
		result := suppressToolResult(ts.ToolName, ts.RawArgs, ts.Result, ts.IsError)
		m.setToolResult(item, result, ts.IsError)
		m.chatList.Append(item)
		return
	}

	m.chatUpdateToolStatus(id, status)
	if ts.Elapsed > 0 {
		m.setToolElapsed(existing, ts.Elapsed)
	}
	result := suppressToolResult(ts.ToolName, ts.RawArgs, ts.Result, ts.IsError)
	m.setToolResult(existing, result, ts.IsError)
}

func (m *Model) chatUpdateToolStatus(id string, status chat.ToolStatus) {
	if m.chatList == nil {
		return
	}
	item := m.chatList.FindByID(id)
	if item == nil {
		return
	}
	if setter, ok := item.(interface{ SetStatus(chat.ToolStatus) }); ok {
		setter.SetStatus(status)
	}
}

func (m *Model) setToolResult(item chat.Item, result string, isError bool) {
	if setter, ok := item.(interface{ SetResult(string, bool) }); ok {
		setter.SetResult(result, isError)
	}
}

func (m *Model) setToolElapsed(item chat.Item, elapsed time.Duration) {
	if setter, ok := item.(interface{ SetElapsed(time.Duration) }); ok {
		setter.SetElapsed(elapsed)
	}
}

func suppressToolResult(toolName, rawArgs, result string, isError bool) string {
	switch toolName {
	case "web_fetch", "web_search":
		return ""
	case "start_command":
		return tool.StartCommandResultText(result, isError)
	case "stop_command", "todo_write", "list_agents":
		return ""
	case "read_command_output":

		return extractRecentOutput(result)
	case "ask_user":
		return formatAskUserResult(result)
	case "multi_file_edit", "multi_edit_file":
		return formatMultiEditResult(result)
	case "team_create":
		return tool.TeamCreateResultText(result)
	case "teammate_spawn":
		return formatTeammateSpawnResult(result)
	case "swarm_task_create":
		return tool.SwarmTaskCreateResultMarkdown(result)
	case "task_create", "task_get", "task_update", "task_list", "task_stop", "task_output",
		"cron_create", "cron_delete", "cron_list", "cron_update", "cron_pause", "cron_resume", "cron_get":
		if present, ok := tool.DescribeToolResult(toolName, rawArgs, result, isError); ok {
			return present.Summary
		}
	case "lanchat":
		if present, ok := tool.DescribeToolResult(toolName, rawArgs, result, isError); ok {
			if present.Payload != "" {
				return present.Payload
			}
			return present.Summary
		}
	}
	return collapseGuidanceHints(result)
}

var guidanceTagLine = regexp.MustCompile(`^\[[A-Z][A-Z0-9-]{1,}\]\s`)

func collapseGuidanceHints(result string) string {
	paras := strings.Split(result, "\n\n")

	if len(paras) < 2 {
		return result
	}
	end := len(paras)
	for end > 0 {
		firstLine := paras[end-1]
		if nl := strings.IndexByte(firstLine, '\n'); nl >= 0 {
			firstLine = firstLine[:nl]
		}
		if !guidanceTagLine.MatchString(strings.TrimRight(firstLine, " \t\r")) {
			break
		}
		end--
	}
	folded := len(paras) - end
	if folded == 0 {
		return result
	}
	var tags []string
	for _, p := range paras[end:] {
		firstLine := p
		if nl := strings.IndexByte(firstLine, '\n'); nl >= 0 {
			firstLine = firstLine[:nl]
		}
		if m := guidanceTagLine.FindStringSubmatch(strings.TrimRight(firstLine, " \t\r")); m != nil {
			tag := strings.Trim(m[0], " []")
			if !slices.Contains(tags, tag) {
				tags = append(tags, tag)
			}
		}
	}
	summary := fmt.Sprintf("ⓘ %d agent guidance block(s) folded (model sees full text): %s",
		folded, strings.Join(tags, ", "))
	if end == 0 {
		return summary
	}
	return strings.Join(paras[:end], "\n\n") + "\n\n" + summary
}

func formatTeammateSpawnResult(result string) string {
	trimmed := strings.TrimSpace(result)
	if trimmed == "" {
		return result
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return result
	}
	name, _ := raw["Name"].(string)
	if name == "" {
		name, _ = raw["name"].(string)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return result
	}
	return fmt.Sprintf("Teammate %s Created", name)
}

func formatMultiEditResult(result string) string {
	var raw struct {
		Summary string `json:"summary"`
		Results []struct {
			Path             string `json:"path"`
			Status           string `json:"status"`
			AppliedEditCount int    `json:"applied_edit_count"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(result), &raw); err != nil {
		return result
	}

	var lines []string
	lines = append(lines, raw.Summary)
	for _, r := range raw.Results {
		path := r.Path
		if idx := strings.LastIndex(path, "/"); idx >= 0 {
			path = path[idx+1:]
		}
		if r.Status == "success" {
			if r.AppliedEditCount > 0 {
				lines = append(lines, fmt.Sprintf("  - %s (%d edit%s)", path, r.AppliedEditCount, pluralS(r.AppliedEditCount)))
			} else {
				lines = append(lines, fmt.Sprintf("  - %s", path))
			}
		} else {
			lines = append(lines, fmt.Sprintf("  - %s [%s]", path, r.Status))
		}
	}
	return strings.Join(lines, "\n")
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func extractRecentOutput(result string) string {
	marker := "Recent output:\n"
	idx := strings.Index(result, marker)
	if idx < 0 {
		return ""
	}
	output := result[idx+len(marker):]
	return strings.TrimSpace(output)
}

func formatAskUserResult(result string) string {
	var resp struct {
		Title   string `json:"title"`
		Answers []struct {
			Title           string   `json:"title"`
			SelectedChoices []string `json:"selected_choices"`
			FreeformText    string   `json:"freeform_text"`
			Answered        bool     `json:"answered"`
		} `json:"answers"`
	}
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		return result
	}

	var b strings.Builder
	for i, ans := range resp.Answers {
		if i > 0 {
			b.WriteString("\n")
		}

		q := ans.Title
		if q == "" {
			q = fmt.Sprintf("Question %d", i+1)
		}
		b.WriteString("Q: ")
		b.WriteString(q)
		b.WriteString("\n")

		var parts []string
		for _, c := range ans.SelectedChoices {
			parts = append(parts, c)
		}
		if ans.FreeformText != "" {
			parts = append(parts, ans.FreeformText)
		}
		b.WriteString("A: ")
		b.WriteString(strings.Join(parts, ", "))
	}
	return strings.TrimSpace(b.String())
}

const todoToolItemID = "todo-list"

func (m *Model) chatUpdateTodoItem(todos []todoStateItem) {
	if m.chatList == nil || len(todos) == 0 {
		return
	}
	tasks := make([]chat.TodoTask, len(todos))
	for i, td := range todos {
		tasks[i] = chat.TodoTask{
			ID:      td.ID,
			Content: td.Content,
			Status:  td.Status,
		}
	}

	existing := m.chatList.FindByID(todoToolItemID)
	if existing != nil {
		if todo, ok := existing.(*chat.TodoToolItem); ok {
			todo.SetTasks(tasks)
			return
		}
	}

	item := chat.NewTodoToolItem(todoToolItemID, tasks, m.chatStyles, string(m.currentLanguage()))
	m.chatList.Append(item)
}

var chatIDCounter int64

func nextChatID() string {
	n := atomic.AddInt64(&chatIDCounter, 1)
	return fmt.Sprintf("chat-%d", n)
}

var sysIDCounter int64

func nextSystemID() string {
	n := atomic.AddInt64(&sysIDCounter, 1)
	return fmt.Sprintf("sys-%d", n)
}

var assistantCounter int64

func (m *Model) currentAssistantID() string {
	return fmt.Sprintf("assistant-%d", atomic.LoadInt64(&assistantCounter))
}

func (m *Model) nextAssistantID() string {
	n := atomic.AddInt64(&assistantCounter, 1)
	return fmt.Sprintf("assistant-%d", n)
}

func (m *Model) chatEnsureAssistant() {
	id := m.currentAssistantID()
	if m.chatList == nil {
		return
	}
	if m.chatList.FindByID(id) != nil {
		return
	}
	m.chatList.Append(chat.NewAssistantItem(id, m.chatStyles))
}

func stripAnsiForChat(s string) string {
	var result strings.Builder
	inEscape := false
	sawBracket := false
	for _, c := range s {
		if !inEscape && c == '\x1b' {
			inEscape = true
			sawBracket = false
			continue
		}
		if inEscape {
			if !sawBracket {
				if c == '[' {
					sawBracket = true
					continue
				}

				inEscape = false
				continue
			}

			if c >= 0x40 && c <= 0x7e {
				inEscape = false
			}
			continue
		}
		result.WriteRune(c)
	}
	return result.String()
}

func extractAgentID(rawArgs string) string {
	var args struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return ""
	}
	return args.AgentID
}

func (m *Model) formatSubAgentDoneNotice(msg subAgentDoneMsg) string {
	name := msg.AgentName
	if name == "" {
		name = msg.AgentID
	}
	if msg.IsError {
		return name + " failed"
	}
	return name + " completed"
}
