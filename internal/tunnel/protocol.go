package tunnel

import "encoding/json"

// GatewayMessage is a JSON message exchanged over the encrypted channel.
type GatewayMessage struct {
	SessionID string          `json:"session_id,omitempty"`
	EventID   string          `json:"event_id,omitempty"`
	StreamID  string          `json:"stream_id,omitempty"`
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data,omitempty"`
}

// Protocol defines the message types exchanged between the ggcode backend
// and mobile clients over the WebSocket tunnel.
//
// Server → Client: event messages (lowercase type)
// Client → Server: command messages (lowercase type)

// Server → Client event types.
const (
	EventConnected          = "connected"
	EventActiveSession      = "active_session"
	EventSessionInfo        = "session_info"
	EventActivity           = "activity"     // main-agent activity text change
	EventUserMessage        = "user_message" // user text from desktop
	EventSystemMessage      = "system_message"
	EventText               = "text"      // streaming text chunk
	EventTextDone           = "text_done" // text stream complete
	EventStatus             = "status"    // agent status change
	EventToolCall           = "tool_call"
	EventToolResult         = "tool_result"
	EventApprovalRequest    = "approval_request"
	EventApprovalResult     = "approval_result"
	EventAskUserRequest     = "ask_user_request"
	EventAskUserResponse    = "ask_user_response"
	EventSubagentSpawn      = "subagent_spawn"
	EventSubagentText       = "subagent_text"
	EventSubagentStatus     = "subagent_status"
	EventSubagentToolCall   = "subagent_tool_call"
	EventSubagentToolResult = "subagent_tool_result"
	EventSubagentComplete   = "subagent_complete"
	EventResumeMiss         = "resume_miss"
	EventSnapshotReset      = "snapshot_reset"
	EventLanguageChange     = "language_change" // bidirectional language sync
	EventError              = "error"
	EventPing               = "ping"
	EventDisconnected       = "disconnected"
)

// Client → Server command types.
const (
	CmdResumeHello      = "resume_hello"
	CmdResumeFrom       = "resume_from"
	CmdMessage          = "message"
	CmdApprovalResponse = "approval_response"
	CmdInterrupt        = "interrupt"
	CmdModeChange       = "mode_change"
	CmdAskUserResponse  = "ask_user_response"
	CmdPong             = "pong"
	CmdLanguageChange   = "language_change"
)

// Agent status values.
const (
	StatusIdle     = "idle"
	StatusBusy     = "busy"
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

// Resume mode values used by resume_ack.
const (
	ResumeModeIncremental      = "incremental"
	ResumeModeFullHistory      = "full_history"
	ResumeModeSnapshotRequired = "snapshot_required"
)

// SessionInfoData carries session metadata sent after connection.
type SessionInfoData struct {
	Workspace string `json:"workspace"`
	Model     string `json:"model"`
	Provider  string `json:"provider"`
	Mode      string `json:"mode"`
	Version   string `json:"version"`
	Language  string `json:"language,omitempty"`
	Theme     string `json:"theme,omitempty"`
}

// ActiveSessionData declares the authoritative TUI/GUI session bound to the
// current tunnel room.
type ActiveSessionData struct {
	SessionID string `json:"session_id"`
}

// LanguageChangeData carries a language preference change (bidirectional sync).
type LanguageChangeData struct {
	Language string `json:"language"` // "en" or "zh-CN"
}

// ThemeChangeData carries a theme change (bidirectional sync).
type ThemeChangeData struct {
	Theme string `json:"theme"` // "midnight", "oled", "nord", "rose", "forest", "light"
}

// EventThemeChange is the event name for theme changes across clients.
const EventThemeChange = "theme_change"

// CmdThemeChange is the command name for theme change requests.
const CmdThemeChange = "theme_change"

// ResumeHelloData requests either incremental replay or full session replay.
type ResumeHelloData struct {
	ClientID    string `json:"client_id"`
	SessionID   string `json:"session_id,omitempty"`
	LastEventID string `json:"last_event_id,omitempty"`
}

// ResumeFromData requests replay starting after the caller's last good cursor.
type ResumeFromData struct {
	ClientID    string `json:"client_id"`
	SessionID   string `json:"session_id,omitempty"`
	LastEventID string `json:"last_event_id,omitempty"`
}

// ResumeAckData tells the client how the gateway will recover state.
type ResumeAckData struct {
	ClientID   string `json:"client_id"`
	SessionID  string `json:"session_id,omitempty"`
	ResumeMode string `json:"resume_mode"`
}

// TextData carries a streaming text chunk.
type TextData struct {
	ID    string `json:"id"`             // message ID to group chunks
	Chunk string `json:"chunk"`          // text content
	Done  bool   `json:"done"`           // true on last chunk
	Kind  string `json:"kind,omitempty"` // optional semantic hint for rendering
}

const (
	MessageKindCron         = "cron"
	MessageKindShellCommand = "shell_command"
	MessageKindShellOutput  = "shell_output"
)

// StatusData carries an agent status change.
type StatusData struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// ActivityData carries the current main-agent activity text.
type ActivityData struct {
	Activity string `json:"activity,omitempty"`
}

// ToolCallData carries a tool call notification.
type ToolCallData struct {
	ToolID      string `json:"tool_id"`
	ToolName    string `json:"tool_name"`
	DisplayName string `json:"display_name,omitempty"`
	Args        string `json:"args,omitempty"`
	Detail      string `json:"detail,omitempty"`
}

// ToolResultData carries a tool result.
type ToolResultData struct {
	ToolID      string `json:"tool_id"`
	ToolName    string `json:"tool_name"`
	Result      string `json:"result,omitempty"`
	Summary     string `json:"summary,omitempty"`
	Payload     string `json:"payload,omitempty"`
	PayloadMode string `json:"payload_mode,omitempty"`
	IsError     bool   `json:"is_error,omitempty"`
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
	Text        string `json:"text"`
	DisplayText string `json:"display_text,omitempty"`
	Kind        string `json:"kind,omitempty"`
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

// SubagentToolCallData carries tool call info from a sub-agent.
type SubagentToolCallData struct {
	AgentID     string `json:"agent_id"`
	ToolID      string `json:"tool_id"`
	ToolName    string `json:"tool_name"`
	DisplayName string `json:"display_name,omitempty"`
	Args        string `json:"args,omitempty"`
	Detail      string `json:"detail,omitempty"`
}

// SubagentToolResultData carries tool result from a sub-agent.
type SubagentToolResultData struct {
	AgentID     string `json:"agent_id"`
	ToolID      string `json:"tool_id"`
	ToolName    string `json:"tool_name"`
	Result      string `json:"result"`
	Summary     string `json:"summary,omitempty"`
	Payload     string `json:"payload,omitempty"`
	PayloadMode string `json:"payload_mode,omitempty"`
	IsError     bool   `json:"is_error"`
}

// SubagentCompleteData notifies that a sub-agent has finished.
type SubagentCompleteData struct {
	AgentID string `json:"agent_id"`
	Name    string `json:"name"`
	Summary string `json:"summary"` // one-line summary of what was done
	Success bool   `json:"success"`
}
