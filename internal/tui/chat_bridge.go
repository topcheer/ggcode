package tui

import (
	"fmt"

	"github.com/topcheer/ggcode/internal/chat"
)

// chatWrite appends an item to the new chatList.
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

// chatStartAssistant creates a streaming assistant entry in chatList.
func (m *Model) chatStartAssistant(id string) {
	m.chatWrite(chat.NewAssistantItem(id, m.chatStyles))
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

// chatListActive returns true when chatList has items and is the primary render path.
// When active, legacy writes to m.output/chatEntries are skipped.
func (m *Model) chatListActive() bool {
	return m.chatList != nil && m.chatList.Len() > 0
}

// legacyWrite writes to m.output and chatEntries — the old rendering path.
// Skipped when chatList is active (new path).
func (m *Model) legacyWrite(text string) {
	if m.chatListActive() {
		return
	}
	m.output.WriteString(text)
}

func (m *Model) legacyWriteEntry(entry ChatEntry) {
	if m.chatListActive() {
		return
	}
	if entry.Prefix != "" && (entry.Role == "user" || entry.Role == "assistant") {
		m.output.WriteString(m.renderConversationUserEntry(entry.Prefix, entry.RawText))
		m.output.WriteString("\n")
	} else if entry.Role == "assistant" {
		// Pure markdown, no prefix
	} else {
		m.output.WriteString(entry.RawText)
	}
	m.chatEntries.Append(entry)
}

// chatListScrollToBottom scrolls conversation to bottom.
// chatList path or viewport path — one or the other, no bridge.
func (m *Model) chatListScrollToBottom() {
	if m.chatList != nil && m.chatList.Len() > 0 {
		m.chatList.ScrollToEnd()
	} else {
		m.viewport.GotoBottom()
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
	// Create a new tool item based on the tool name
	input := ts.RawArgs
	if input == "" {
		input = ts.Args
	}
	if input == "" && ts.Detail != "" {
		input = fmt.Sprintf(`{"path":"%s"}`, ts.Detail)
	}
	item := chat.NewToolItem(id, ts.ToolName, chat.StatusRunning, input, m.chatStyles)
	m.chatList.Append(item)
}

// chatFinishTool marks a tool item as finished with result.
func (m *Model) chatFinishTool(ts ToolStatusMsg) {
	if m.chatList == nil {
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
		input := ts.RawArgs
		if input == "" {
			input = ts.Args
		}
		item := chat.NewToolItem(id, ts.ToolName, status, input, m.chatStyles)
		// Set result on the appropriate type
		m.setToolResult(item, ts.Result)
		m.chatList.Append(item)
		return
	}

	// Update existing item
	m.chatUpdateToolStatus(id, status)
	m.setToolResult(existing, ts.Result)
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

// bridgeDualWriteSystem writes to legacy output + old chatEntries only.
// System/compaction/status lines are NOT added to chatList — they are
// rendering noise that would pollute the conversation view.
// Only semantic messages (user, assistant, tool, todo) go into chatList.
func (m *Model) bridgeDualWriteSystem(text string) {
	if m.chatListActive() {
		return
	}
	m.output.WriteString(text)
	m.chatEntries.Append(ChatEntry{Role: "system", RawText: text})
}
