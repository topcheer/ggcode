package tui

import (
	"fmt"
	"strings"

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

// chatStartTool adds a running tool item to chatList, or updates an existing one.
func (m *Model) chatStartTool(ts ToolStatusMsg) {
	if m.chatList == nil || isSubAgentLifecycleTool(ts.ToolName) {
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
	item := chat.NewToolItem(id, ts.ToolName, chat.StatusRunning, input, m.chatStyles)
	m.chatList.Append(item)
}

// chatFinishTool marks a tool item as finished with result.
func (m *Model) chatFinishTool(ts ToolStatusMsg) {
	if m.chatList == nil || isSubAgentLifecycleTool(ts.ToolName) {
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
	switch v := item.(type) {
	case *chat.BaseToolItem:
		v.SetStatus(status)
	case *chat.BashToolItem:
		v.SetStatus(status)
	case *chat.FileToolItem:
		v.SetStatus(status)
	case *chat.SearchToolItem:
		v.SetStatus(status)
	case *chat.GenericToolItem:
		v.SetStatus(status)
	}
}

// setToolResult sets the result on the appropriate tool item type.
func (m *Model) setToolResult(item chat.Item, result string) {
	switch v := item.(type) {
	case *chat.BaseToolItem:
		v.SetResult(result, false)
	case *chat.BashToolItem:
		v.SetResult(result, false)
	case *chat.FileToolItem:
		v.SetResult(result, false)
	case *chat.SearchToolItem:
		v.SetResult(result, false)
	case *chat.GenericToolItem:
		v.SetResult(result, false)
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

// assistantStreamingID is the fixed ID for the current streaming assistant.
const assistantStreamingID = "assistant-streaming"

// bridgeDualWrite writes to both old chatEntries and new chatList.
// Call this during migration where dualWrite was previously used.
// Once migration is complete, this function and all chatEntries code can be removed.
func (m *Model) bridgeDualWrite(entry ChatEntry) {
	// Old path
	if entry.Prefix != "" && (entry.Role == "user" || entry.Role == "assistant") {
		m.output.WriteString(m.renderConversationUserEntry(entry.Prefix, entry.RawText))
		m.output.WriteString("\n")
	} else if entry.Role == "assistant" {
		// Pure markdown, no prefix
	} else {
		m.output.WriteString(entry.RawText)
	}
	m.chatEntries.Append(entry)

	// New path — also write to chatList
	if m.chatList == nil {
		return
	}
	switch entry.Role {
	case "user":
		m.chatWriteUser(nextChatID(), entry.RawText)
	case "assistant":
		if entry.Streaming {
			if m.chatList.FindByID(assistantStreamingID) == nil {
				m.chatStartAssistant(assistantStreamingID)
			}
			m.chatUpdateAssistantText(assistantStreamingID, entry.RawText)
		} else {
			m.chatUpdateAssistantText(assistantStreamingID, entry.RawText)
			m.chatFinishAssistant(assistantStreamingID)
		}
	case "system":
		m.chatWriteSystem(nextSystemID(), strings.TrimSpace(entry.RawText))
	case "tool":
		m.chatWriteSystem(nextSystemID(), strings.TrimSpace(entry.RawText))
	}
}

// bridgeDualWriteSystem writes a pre-rendered system string to all paths.
func (m *Model) bridgeDualWriteSystem(text string) {
	m.output.WriteString(text)
	m.chatEntries.Append(ChatEntry{Role: "system", RawText: text})
	m.chatWriteSystem(nextSystemID(), text)
}
