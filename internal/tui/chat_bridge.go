package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/chat"
)

// chatWrite appends an item to chatList.
func (m *Model) chatWrite(item chat.Item) {
	if m.chatList != nil {
		m.chatList.Append(item)
	}
}

// chatWriteUser appends a user message to chatList.
func (m *Model) chatWriteUser(id, text string) {
	m.chatWrite(chat.NewUserItem(id, text, m.chatStyles))
}

// chatWriteSystem appends a system/status message to chatList.
func (m *Model) chatWriteSystem(id, text string) {
	m.chatWrite(chat.NewSystemItem(id, text, m.chatStyles))
}

// chatUpdateAssistantText updates the streaming assistant text.
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

// chatFinishAssistant marks the assistant as done streaming.
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

// chatReset clears the chatList.
func (m *Model) chatReset() {
	if m.chatList != nil {
		m.chatList.SetItems(nil)
	}
}

// chatListScrollToBottom scrolls conversation to bottom.
func (m *Model) chatListScrollToBottom() {
	if m.chatList != nil {
		m.chatList.ScrollToEnd()
	}
}

func (m *Model) chatStartTool(ts ToolStatusMsg) {
	if m.chatList == nil {
		return
	}

	// spawn_agent → create AgentToolItem
	if ts.ToolName == "spawn_agent" {
		id := ts.ToolID
		if id == "" {
			id = nextChatID()
		}
		taskDisplay := ts.Detail
		if taskDisplay == "" {
			taskDisplay = ts.Args
		}
		item := chat.NewAgentToolItem(id, taskDisplay, chat.StatusRunning, m.chatStyles)
		m.chatWrite(item)
		return
	}

	// wait_agent / list_agents → update spawn_agent status
	if isSubAgentLifecycleTool(ts.ToolName) {
		return
	}
	id := ts.ToolID
	if id == "" {
		id = nextChatID()
	}
	existing := m.chatList.FindByID(id)
	if existing != nil {
		// Already tracked — just update status
		m.chatUpdateToolStatus(id, chat.StatusRunning)
		return
	}
	// Resolve display name and detail from describeTool when not provided.
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
	// Create a new tool item based on the tool name
	ctx := chat.ToolContext{
		ToolName:    ts.ToolName,
		DisplayName: displayName,
		Detail:      detail,
		RawArgs:     ts.RawArgs,
	}
	item := chat.NewToolItem(id, ctx, chat.StatusRunning, m.chatStyles)
	m.chatList.Append(item)
}

// chatFinishTool marks a tool item as finished with result.
func (m *Model) chatFinishTool(ts ToolStatusMsg) {
	if m.chatList == nil {
		return
	}

	// todo_write finish → apply todo state
	if ts.ToolName == "todo_write" {
		m.applyTodoWrite(ts)
		return
	}

	// spawn_agent finish → mark AgentToolItem as done
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
		m.setToolResult(item, ts.Result)
		return
	}

	if isSubAgentLifecycleTool(ts.ToolName) {
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
		// Not tracked yet — create a finished item
		// Resolve display name and detail from describeTool when not provided.
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
		}
		item := chat.NewToolItem(id, ctx, status, m.chatStyles)
		result := suppressToolResult(ts.ToolName, ts.Result)
		m.setToolResult(item, result)
		m.chatList.Append(item)
		return
	}

	// Update existing item
	m.chatUpdateToolStatus(id, status)
	result := suppressToolResult(ts.ToolName, ts.Result)
	m.setToolResult(existing, result)
}

// chatUpdateToolStatus updates the status of a tool item.
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

// setToolResult sets the result on the appropriate tool item type.
func (m *Model) setToolResult(item chat.Item, result string) {
	if setter, ok := item.(interface{ SetResult(string, bool) }); ok {
		setter.SetResult(result, false)
	}
}

// suppressToolResult returns empty string for tools whose result body should
// not be rendered inline in the TUI chat list.
func suppressToolResult(toolName, result string) string {
	switch toolName {
	case "web_fetch", "web_search":
		return ""
	case "start_command", "stop_command":
		return ""
	case "read_command_output":
		// Only keep the actual output content, strip structured metadata
		return extractRecentOutput(result)
	case "ask_user":
		return formatAskUserResult(result)
	}
	return result
}

// extractRecentOutput parses the structured read_command_output result
// and returns only the "Recent output" content.
func extractRecentOutput(result string) string {
	marker := "Recent output:\n"
	idx := strings.Index(result, marker)
	if idx < 0 {
		return ""
	}
	output := result[idx+len(marker):]
	return strings.TrimSpace(output)
}

// formatAskUserResult converts the JSON ask_user response into a human-readable
// format showing each question title and the user's answer.
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
		// Question
		q := ans.Title
		if q == "" {
			q = fmt.Sprintf("Question %d", i+1)
		}
		b.WriteString("Q: ")
		b.WriteString(q)
		b.WriteString("\n")
		// Answer: selected choices first, then freeform text
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

// todoToolItemID is the fixed ID for the persistent todo list in chatList.
const todoToolItemID = "todo-list"

// chatUpdateTodoItem updates or creates the TodoToolItem in chatList.
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
	// Create new
	item := chat.NewTodoToolItem(todoToolItemID, tasks, m.chatStyles)
	m.chatList.Append(item)
}

// nextChatID generates a unique ID for chat items.
var chatIDCounter int64

func nextChatID() string {
	chatIDCounter++
	return fmt.Sprintf("chat-%d", chatIDCounter)
}

// nextSystemID generates a unique ID for system messages.
var sysIDCounter int64

func nextSystemID() string {
	sysIDCounter++
	return fmt.Sprintf("sys-%d", sysIDCounter)
}

// assistantCounter generates unique IDs for each assistant turn.
var assistantCounter int64

// currentAssistantID returns the ID of the current streaming assistant item,
// creating a new one if needed. Each call to nextAssistantID advances to a
// fresh ID for a new turn.
func (m *Model) currentAssistantID() string {
	return fmt.Sprintf("assistant-%d", assistantCounter)
}

func (m *Model) nextAssistantID() string {
	assistantCounter++
	return fmt.Sprintf("assistant-%d", assistantCounter)
}

// chatEnsureAssistant creates a new streaming assistant item if one doesn't
// exist for the current turn, or starts a fresh one.
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

// stripAnsiForChat removes ANSI escape codes from text.
func stripAnsiForChat(s string) string {
	var result strings.Builder
	inEscape := false
	for _, c := range s {
		if c == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if c == 'm' {
				inEscape = false
			}
			continue
		}
		result.WriteRune(c)
	}
	return result.String()
}
