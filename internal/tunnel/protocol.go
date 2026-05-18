package tunnel

// Protocol defines the message types exchanged between the ggcode backend
// and mobile clients over the WebSocket tunnel.
//
// Server → Client: event messages (lowercase type)
// Client → Server: command messages (lowercase type)

// Server → Client event types.
const (
	EventConnected       = "connected"
	EventSessionInfo     = "session_info"
	EventText            = "text"      // streaming text chunk
	EventTextDone        = "text_done" // text stream complete
	EventStatus          = "status"    // agent status change
	EventToolCall        = "tool_call"
	EventToolResult      = "tool_result"
	EventApprovalRequest = "approval_request"
	EventApprovalResult  = "approval_result"
	EventError           = "error"
	EventPing            = "ping"
	EventDisconnected    = "disconnected"
)

// Client → Server command types.
const (
	CmdMessage          = "message"
	CmdApprovalResponse = "approval_response"
	CmdInterrupt        = "interrupt"
	CmdModeChange       = "mode_change"
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
