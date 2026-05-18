package tunnel

import "encoding/json"

// GatewayMessage is a JSON message exchanged over the encrypted channel.
type GatewayMessage struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// Protocol defines the message types exchanged between the ggcode backend
// and mobile clients over the WebSocket tunnel.
//
// Server → Client: event messages (lowercase type)
// Client → Server: command messages (lowercase type)

// Server → Client event types.
const (
	EventConnected        = "connected"
	EventSessionInfo      = "session_info"
	EventUserMessage      = "user_message" // user text from desktop
	EventText             = "text"         // streaming text chunk
	EventTextDone         = "text_done"    // text stream complete
	EventStatus           = "status"       // agent status change
	EventToolCall         = "tool_call"
	EventToolResult       = "tool_result"
	EventApprovalRequest  = "approval_request"
	EventApprovalResult   = "approval_result"
	EventAskUserRequest   = "ask_user_request"
	EventAskUserResponse  = "ask_user_response"
	EventSubagentSpawn    = "subagent_spawn"
	EventSubagentText     = "subagent_text"
	EventSubagentStatus   = "subagent_status"
	EventSubagentComplete = "subagent_complete"
	EventError            = "error"
	EventPing             = "ping"
	EventDisconnected     = "disconnected"
)

// Client → Server command types.
const (
	CmdMessage          = "message"
	CmdApprovalResponse = "approval_response"
	CmdInterrupt        = "interrupt"
	CmdModeChange       = "mode_change"
	CmdAskUserResponse  = "ask_user_response"
	CmdPong             = "pong"
)

// Agent status values.
const (
	StatusIdle     = "idle"
	StatusThinking = "thinking"
	StatusRunning  = "running"
	StatusWaiting  = "waiting" // waiting for approval
	StatusError    = "error"
)

// Mode values.
const (
	ModeSupervised = "supervised"
	ModeAuto       = "auto"
	ModeBypass     = "bypass"
	ModeAutopilot  = "autopilot"
)

// Approval decisions.
const (
	DecisionAllow       = "allow"
	DecisionDeny        = "deny"
	DecisionAlwaysAllow = "always_allow"
)

// SessionInfoData carries session metadata sent after connection.
type SessionInfoData struct {
	Workspace string `json:"workspace"`
	Model     string `json:"model"`
	Provider  string `json:"provider"`
	Mode      string `json:"mode"`
	Version   string `json:"version"`
}

// TextData carries a streaming text chunk.
type TextData struct {
	ID    string `json:"id"`    // message ID to group chunks
	Chunk string `json:"chunk"` // text content
	Done  bool   `json:"done"`  // true on last chunk
}

// StatusData carries an agent status change.
type StatusData struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// ToolCallData carries a tool call notification.
type ToolCallData struct {
	ToolName string `json:"tool_name"`
	Args     string `json:"args,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

// ToolResultData carries a tool result.
type ToolResultData struct {
	ToolName string `json:"tool_name"`
	Result   string `json:"result,omitempty"`
	IsError  bool   `json:"is_error,omitempty"`
}

// ApprovalRequestData carries an approval request to the mobile client.
type ApprovalRequestData struct {
	ID       string `json:"id"`
	ToolName string `json:"tool_name"`
	Input    string `json:"input"`
}

// ApprovalResponseData carries the user's approval decision back.
type ApprovalResponseData struct {
	ID       string `json:"id"`
	Decision string `json:"decision"` // allow/deny/always_allow
}

// MessageData carries a user message.
type MessageData struct {
	Text string `json:"text"`
}

// ModeChangeData carries a mode change request.
type ModeChangeData struct {
	Mode string `json:"mode"`
}

// ErrorData carries an error.
type ErrorData struct {
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
}

// ─── Ask User (structured questionnaire) ───

// AskUserRequestData carries a structured questionnaire from the agent.
// This is NOT the same as approval_request — it's a multi-question form
// with single/multi/text question types.
type AskUserRequestData struct {
	ID        string            `json:"id"`
	Title     string            `json:"title,omitempty"`
	Questions []AskUserQuestion `json:"questions"`
}

// AskUserQuestion represents a single question in the questionnaire.
type AskUserQuestion struct {
	ID            string          `json:"id"`
	Prompt        string          `json:"prompt"`
	Kind          string          `json:"kind"` // single/multi/text
	Choices       []AskUserChoice `json:"choices,omitempty"`
	AllowFreeform bool            `json:"allow_freeform,omitempty"`
	Placeholder   string          `json:"placeholder,omitempty"`
}

// AskUserChoice represents a selectable choice.
type AskUserChoice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// AskUserResponseData carries the user's answers back.
type AskUserResponseData struct {
	ID      string          `json:"id"`
	Status  string          `json:"status"` // submitted/cancelled
	Answers []AskUserAnswer `json:"answers,omitempty"`
}

// AskUserAnswer carries a single question answer.
type AskUserAnswer struct {
	QuestionID   string   `json:"question_id"`
	ChoiceIDs    []string `json:"choice_ids,omitempty"`
	FreeformText string   `json:"freeform_text,omitempty"`
}

// ─── Sub-agent / Teammate ───

// SubagentSpawnData notifies mobile that a sub-agent has been spawned.
// Mobile should show a live activity card for this agent.
type SubagentSpawnData struct {
	AgentID  string `json:"agent_id"`
	Name     string `json:"name"`                // e.g. "Researcher", "Coder"
	Task     string `json:"task"`                // brief task description
	Color    string `json:"color,omitempty"`     // e.g. "#4CAF50"
	ParentID string `json:"parent_id,omitempty"` // empty for top-level
}

// SubagentTextData carries streaming text from a sub-agent.
type SubagentTextData struct {
	AgentID string `json:"agent_id"`
	ID      string `json:"id"` // message ID for grouping chunks
	Chunk   string `json:"chunk"`
	Done    bool   `json:"done"`
}

// SubagentStatusData carries status updates for a sub-agent.
type SubagentStatusData struct {
	AgentID string `json:"agent_id"`
	Status  string `json:"status"` // running/waiting_approval/completed/failed
	Message string `json:"message,omitempty"`
}

// SubagentCompleteData notifies that a sub-agent has finished.
type SubagentCompleteData struct {
	AgentID string `json:"agent_id"`
	Name    string `json:"name"`
	Summary string `json:"summary"` // one-line summary of what was done
	Success bool   `json:"success"`
}
