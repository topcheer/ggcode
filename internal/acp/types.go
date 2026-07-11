package acp

import "encoding/json"

// ---------------------------------------------------------------------------
// JSON-RPC 2.0 base types
// ---------------------------------------------------------------------------

// RequestId is a JSON-RPC request identifier (string or integer).
type RequestID = interface{} // string | int

type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"` // always "2.0"
	ID      RequestID       `json:"id"`      // nil for notifications
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC   string          `json:"jsonrpc"`
	ID        RequestID       `json:"id"`
	Result    interface{}     `json:"result,omitempty"`
	RawResult json.RawMessage `json:"-"` // populated by ReadAnyMessage for SendRequest
	Error     *JSONRPCError   `json:"error,omitempty"`
}

type JSONRPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

type JSONRPCNotification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// ---------------------------------------------------------------------------
// ACP Protocol version
// ---------------------------------------------------------------------------

const ProtocolVersion = 1

// ---------------------------------------------------------------------------
// Initialization
// ---------------------------------------------------------------------------

// InitializeRequest is sent by the client to establish connection.
type InitializeRequest struct {
	Meta               json.RawMessage     `json:"_meta,omitempty"`
	ProtocolVersion    int                 `json:"protocolVersion"`
	ClientCapabilities ClientCapabilities  `json:"clientCapabilities"`
	ClientInfo         *ImplementationInfo `json:"clientInfo,omitempty"`
}

// InitializeResponse is the agent's response to initialize.
type InitializeResponse struct {
	Meta              json.RawMessage    `json:"_meta,omitempty"`
	ProtocolVersion   int                `json:"protocolVersion"`
	AgentCapabilities AgentCapabilities  `json:"agentCapabilities"`
	AgentInfo         ImplementationInfo `json:"agentInfo"`
	AuthMethods       []AuthMethod       `json:"authMethods"`
}

// ---------------------------------------------------------------------------
// Capabilities
// ---------------------------------------------------------------------------

type ClientCapabilities struct {
	Meta     json.RawMessage `json:"_meta,omitempty"`
	FS       *FSCapability   `json:"fs,omitempty"`
	Terminal bool            `json:"terminal,omitempty"`
}

type FSCapability struct {
	ReadTextFile  bool `json:"readTextFile"`
	WriteTextFile bool `json:"writeTextFile"`
}

type AgentCapabilities struct {
	Meta                json.RawMessage      `json:"_meta,omitempty"`
	LoadSession         bool                 `json:"loadSession"`
	PromptCapabilities  *PromptCapabilities  `json:"promptCapabilities,omitempty"`
	MCPCapabilities     *MCPCapabilities     `json:"mcpCapabilities,omitempty"`
	SessionCapabilities *SessionCapabilities `json:"sessionCapabilities,omitempty"`
}

type PromptCapabilities struct {
	Meta            json.RawMessage `json:"_meta,omitempty"`
	Image           bool            `json:"image"`
	Audio           bool            `json:"audio"`
	EmbeddedContext bool            `json:"embeddedContext"`
}

type MCPCapabilities struct {
	Meta json.RawMessage `json:"_meta,omitempty"`
	HTTP bool            `json:"http"`
	SSE  bool            `json:"sse"`
}

// SessionCapabilities describes which optional session methods the agent supports.
type SessionCapabilities struct {
	Meta   json.RawMessage            `json:"_meta,omitempty"`
	Close  *SessionCloseCapabilities  `json:"close,omitempty"`
	List   *SessionListCapabilities   `json:"list,omitempty"`
	Resume *SessionResumeCapabilities `json:"resume,omitempty"`
}

type SessionCloseCapabilities struct {
	Meta json.RawMessage `json:"_meta,omitempty"`
}

type SessionListCapabilities struct {
	Meta json.RawMessage `json:"_meta,omitempty"`
}

type SessionResumeCapabilities struct {
	Meta json.RawMessage `json:"_meta,omitempty"`
}

// ---------------------------------------------------------------------------
// Implementation info
// ---------------------------------------------------------------------------

type ImplementationInfo struct {
	Meta    json.RawMessage `json:"_meta,omitempty"`
	Name    string          `json:"name"`
	Title   string          `json:"title,omitempty"`
	Version string          `json:"version"`
}

// ---------------------------------------------------------------------------
// Authentication
// ---------------------------------------------------------------------------

type AuthMethod struct {
	Meta        json.RawMessage `json:"_meta,omitempty"`
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Type        string          `json:"type,omitempty"` // "agent" | "env_var" | "terminal"
	Vars        []AuthEnvVar    `json:"vars,omitempty"`
	Link        string          `json:"link,omitempty"`
	Args        []string        `json:"args,omitempty"`
	Env         []EnvVariable   `json:"env,omitempty"`
}

type AuthEnvVar struct {
	Meta     json.RawMessage `json:"_meta,omitempty"`
	Name     string          `json:"name"`
	Label    string          `json:"label,omitempty"`
	Secret   *bool           `json:"secret,omitempty"`
	Optional *bool           `json:"optional,omitempty"`
}

// AuthenticateRequest for the authenticate method.
type AuthenticateRequest struct {
	Meta         json.RawMessage `json:"_meta,omitempty"`
	AuthMethodID string          `json:"authMethodId"`
}

// AuthenticateResponse for the authenticate method response.
type AuthenticateResponse struct {
	Meta json.RawMessage `json:"_meta,omitempty"`
}

// ---------------------------------------------------------------------------
// Session management
// ---------------------------------------------------------------------------

// NewSessionRequest creates a new conversation session.
type NewSessionRequest struct {
	Meta       json.RawMessage `json:"_meta,omitempty"`
	CWD        string          `json:"cwd"`
	MCPServers []MCPServer     `json:"mcpServers"`
}

func (r NewSessionRequest) MarshalJSON() ([]byte, error) {
	type alias NewSessionRequest
	aux := alias(r)
	if aux.MCPServers == nil {
		aux.MCPServers = []MCPServer{}
	}
	return json.Marshal(aux)
}

// NewSessionResponse returns session info including available modes and config.
type NewSessionResponse struct {
	Meta          json.RawMessage       `json:"_meta,omitempty"`
	SessionID     string                `json:"sessionId"`
	Modes         *SessionModeState     `json:"modes,omitempty"`
	ConfigOptions []SessionConfigOption `json:"configOptions,omitempty"`
}

// CloseSessionRequest closes an active session.
type CloseSessionRequest struct {
	Meta      json.RawMessage `json:"_meta,omitempty"`
	SessionID string          `json:"sessionId"`
}

// CloseSessionResponse is the response from closing a session.
type CloseSessionResponse struct {
	Meta json.RawMessage `json:"_meta,omitempty"`
}

// ListSessionsRequest lists existing sessions.
type ListSessionsRequest struct {
	Meta   json.RawMessage `json:"_meta,omitempty"`
	Cursor string          `json:"cursor,omitempty"`
	CWD    string          `json:"cwd,omitempty"`
}

// ListSessionsResponse returns session info list.
type ListSessionsResponse struct {
	Meta       json.RawMessage `json:"_meta,omitempty"`
	Sessions   []SessionInfo   `json:"sessions"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

// SessionInfo is summary info about a session.
type SessionInfo struct {
	Meta      json.RawMessage `json:"_meta,omitempty"`
	SessionID string          `json:"sessionId"`
	CWD       string          `json:"cwd,omitempty"`
	Title     string          `json:"title,omitempty"`
	CreatedAt string          `json:"createdAt,omitempty"`
	UpdatedAt string          `json:"updatedAt,omitempty"`
}

// ResumeSessionRequest resumes an existing session.
type ResumeSessionRequest struct {
	Meta       json.RawMessage `json:"_meta,omitempty"`
	SessionID  string          `json:"sessionId"`
	CWD        string          `json:"cwd,omitempty"`
	MCPServers []MCPServer     `json:"mcpServers,omitempty"`
}

// ResumeSessionResponse returns session info after resume.
type ResumeSessionResponse struct {
	Meta          json.RawMessage       `json:"_meta,omitempty"`
	Modes         *SessionModeState     `json:"modes,omitempty"`
	ConfigOptions []SessionConfigOption `json:"configOptions,omitempty"`
}

// ---------------------------------------------------------------------------
// Session modes
// ---------------------------------------------------------------------------

// SessionModeId identifies a session mode.
type SessionModeId = string

// SessionMode describes an available operating mode.
type SessionMode struct {
	Meta        json.RawMessage `json:"_meta,omitempty"`
	ID          SessionModeId   `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
}

// SessionModeState contains the list of available modes and the active one.
type SessionModeState struct {
	Modes   []SessionMode `json:"modes"`
	Current SessionModeId `json:"current"`
}

// SetSessionModeRequest changes the active session mode.
type SetSessionModeRequest struct {
	Meta      json.RawMessage `json:"_meta,omitempty"`
	SessionID string          `json:"sessionId"`
	Mode      SessionModeId   `json:"mode"`
}

// SetSessionModeResponse is the response for set_mode.
type SetSessionModeResponse struct {
	Meta json.RawMessage `json:"_meta,omitempty"`
}

// ---------------------------------------------------------------------------
// Session config options
// ---------------------------------------------------------------------------

// SessionConfigId identifies a configuration option.
type SessionConfigId = string

// SessionConfigValueId identifies a value within a config option.
type SessionConfigValueId = string

// SessionConfigGroupId identifies a group of config values.
type SessionConfigGroupId = string

// SessionConfigOption is a session configuration selector.
// Type discriminator: currently only "select" is defined.
type SessionConfigOption struct {
	Meta         json.RawMessage            `json:"_meta,omitempty"`
	Type         string                     `json:"type"` // "select"
	ID           SessionConfigId            `json:"id"`
	Name         string                     `json:"name"`
	Description  string                     `json:"description,omitempty"`
	Category     string                     `json:"category,omitempty"` // "mode", "model", "thought_level", or other string
	CurrentValue SessionConfigValueId       `json:"currentValue"`
	Options      SessionConfigSelectOptions `json:"options"`
}

// SessionConfigSelectOptions is either a flat list or grouped list.
type SessionConfigSelectOptions []interface{} // SessionConfigSelectOption | SessionConfigSelectGroup

// SessionConfigSelectOption is a single selectable value.
type SessionConfigSelectOption struct {
	Meta    json.RawMessage      `json:"_meta,omitempty"`
	ID      SessionConfigValueId `json:"id"`
	Name    string               `json:"name"`
	GroupID SessionConfigGroupId `json:"groupId,omitempty"`
}

// SessionConfigSelectGroup is a named group of options.
type SessionConfigSelectGroup struct {
	Meta    json.RawMessage             `json:"_meta,omitempty"`
	ID      SessionConfigGroupId        `json:"id"`
	Name    string                      `json:"name"`
	Options []SessionConfigSelectOption `json:"options"`
}

// SetSessionConfigOptionRequest sets a config option value.
type SetSessionConfigOptionRequest struct {
	Meta      json.RawMessage      `json:"_meta,omitempty"`
	SessionID string               `json:"sessionId"`
	ConfigID  SessionConfigId      `json:"configId"`
	Value     SessionConfigValueId `json:"value"`
}

// SetSessionConfigOptionResponse returns updated config options.
type SetSessionConfigOptionResponse struct {
	Meta          json.RawMessage       `json:"_meta,omitempty"`
	ConfigOptions []SessionConfigOption `json:"configOptions"`
}

// ---------------------------------------------------------------------------
// Prompt turn
// ---------------------------------------------------------------------------

// PromptRequest sends a user prompt to the agent.
type PromptRequest struct {
	Meta      json.RawMessage `json:"_meta,omitempty"`
	SessionID string          `json:"sessionId"`
	Prompt    []ContentBlock  `json:"prompt"`
}

// PromptResponse contains the stop reason.
type PromptResponse struct {
	Meta       json.RawMessage `json:"_meta,omitempty"`
	StopReason StopReason      `json:"stopReason"`
}

// PromptCompleteNotification is emitted after the asynchronous ACP prompt loop
// actually finishes. The session/prompt RPC still returns immediately.
type PromptCompleteNotification struct {
	Meta      json.RawMessage `json:"_meta,omitempty"`
	SessionID string          `json:"sessionId"`
	Response  PromptResponse  `json:"response"`
}

// StopReason describes why the agent stopped processing.
type StopReason string

const (
	StopReasonEndTurn   StopReason = "end_turn"
	StopReasonMaxTokens StopReason = "max_tokens"
	StopReasonMaxTurns  StopReason = "max_turns"
	StopReasonCancelled StopReason = "cancelled"
	StopReasonError     StopReason = "error"
	StopReasonToolUse   StopReason = "tool_use"
)

// CancelNotification cancels ongoing session work.
type CancelNotification struct {
	Meta      json.RawMessage `json:"_meta,omitempty"`
	SessionID string          `json:"sessionId"`
}

// ---------------------------------------------------------------------------
// Content blocks
// ---------------------------------------------------------------------------

// ContentBlock represents displayable information.
type ContentBlock struct {
	Type      string            `json:"type"` // "text", "image", "audio", "resource", "resource_link", "tool_use", "tool_result"
	Meta      json.RawMessage   `json:"_meta,omitempty"`
	Text      string            `json:"text,omitempty"`
	ImageURL  string            `json:"imageUrl,omitempty"`
	ImageMIME string            `json:"imageMime,omitempty"`
	ImageData string            `json:"imageData,omitempty"`
	AudioURL  string            `json:"audioUrl,omitempty"`
	AudioMIME string            `json:"audioMime,omitempty"`
	AudioData string            `json:"audioData,omitempty"`
	Resource  *EmbeddedResource `json:"resource,omitempty"`
	URI       string            `json:"uri,omitempty"` // for resource_link
	ToolName  string            `json:"toolName,omitempty"`
	ToolID    string            `json:"toolId,omitempty"`
	Input     json.RawMessage   `json:"input,omitempty"`
	Output    string            `json:"output,omitempty"`
	IsError   bool              `json:"isError,omitempty"`
}

// EmbeddedResource contains resource content that can be embedded.
type EmbeddedResource struct {
	Meta json.RawMessage       `json:"_meta,omitempty"`
	Text *TextResourceContents `json:"text,omitempty"`
	Blob *BlobResourceContents `json:"blob,omitempty"`
}

// TextResourceContents for text-based resources.
type TextResourceContents struct {
	Meta json.RawMessage `json:"_meta,omitempty"`
	URI  string          `json:"uri"`
	Text string          `json:"text"`
}

// BlobResourceContents for binary resources.
type BlobResourceContents struct {
	Meta     json.RawMessage `json:"_meta,omitempty"`
	URI      string          `json:"uri"`
	Blob     string          `json:"blob"` // base64
	MIMEType string          `json:"mimeType,omitempty"`
}

// ---------------------------------------------------------------------------
// Session updates (Agent → Client notifications)
// ---------------------------------------------------------------------------

// SessionNotification is a session/update notification.
type SessionNotification struct {
	Meta      json.RawMessage `json:"_meta,omitempty"`
	SessionID string          `json:"sessionId"`
	Update    SessionUpdate   `json:"update"`
}

// SessionUpdate is a flattened discriminated union for session/update notifications.
// Per ACP spec, all fields are direct children of the update object — no nesting.
type SessionUpdate struct {
	Type string          `json:"sessionUpdate"` // discriminator: agent_message_chunk, tool_call, tool_call_update, plan, etc.
	Meta json.RawMessage `json:"_meta,omitempty"`

	// Content: polymorphic — single ContentBlock for messages, or []ToolCallContentEntry for tool_call_update.
	// Using interface{} because ACP spec reuses the "content" key for both shapes.
	Content interface{} `json:"content,omitempty"`

	// Tool call fields (tool_call, tool_call_update) — flattened, not nested
	ToolCallID string             `json:"toolCallId,omitempty"`
	Title      string             `json:"title,omitempty"`
	Kind       ToolKind           `json:"kind,omitempty"`
	Status     ToolCallStatus     `json:"status,omitempty"`
	Locations  []ToolCallLocation `json:"locations,omitempty"`
	RawInput   json.RawMessage    `json:"rawInput,omitempty"`
	RawOutput  json.RawMessage    `json:"rawOutput,omitempty"`

	// Plan fields
	Plan *Plan `json:"plan,omitempty"`
}

// Session update type constants.
const (
	UpdateAgentMessageChunk = "agent_message_chunk"
	UpdateUserMessageChunk  = "user_message_chunk"
	UpdateToolCall          = "tool_call"
	UpdateToolCallUpdate    = "tool_call_update"
	UpdatePlan              = "plan"
)

// ToolCallContentEntry is a content item within a tool call update.
type ToolCallContentEntry struct {
	Meta    json.RawMessage `json:"_meta,omitempty"`
	Type    string          `json:"type"`              // "content", "diff", "terminal"
	Content *ContentBlock   `json:"content,omitempty"` // for type="content"
	// Diff fields (type="diff")
	Path    string `json:"path,omitempty"`
	OldText string `json:"oldText,omitempty"`
	NewText string `json:"newText,omitempty"`
	// Terminal fields (type="terminal")
	TerminalID string `json:"terminalId,omitempty"`
}

// ---------------------------------------------------------------------------
// Tool calls
// ---------------------------------------------------------------------------

// ToolCallId identifies a tool call within a session.
type ToolCallId = string

// ToolKind categorizes a tool.
type ToolKind string

const (
	ToolKindRead    ToolKind = "read"
	ToolKindEdit    ToolKind = "edit"
	ToolKindExecute ToolKind = "execute"
	ToolKindSearch  ToolKind = "search"
	ToolKindBrowser ToolKind = "browser"
	ToolKindThink   ToolKind = "think"
	ToolKindOther   ToolKind = "other"
)

// ToolCallStatus tracks tool call lifecycle.
type ToolCallStatus string

const (
	ToolCallStatusPending    ToolCallStatus = "pending"
	ToolCallStatusInProgress ToolCallStatus = "in_progress"
	ToolCallStatusCompleted  ToolCallStatus = "completed"
	ToolCallStatusFailed     ToolCallStatus = "failed"
)

// ToolCallUpdate reports progress on a tool call.
type ToolCallUpdate struct {
	Meta       json.RawMessage    `json:"_meta,omitempty"`
	ToolCallID ToolCallId         `json:"toolCallId"`
	Title      string             `json:"title,omitempty"`
	Kind       ToolKind           `json:"kind,omitempty"`
	Status     ToolCallStatus     `json:"status,omitempty"`
	Content    *ToolCallContent   `json:"content,omitempty"`
	RawInput   json.RawMessage    `json:"rawInput,omitempty"`
	RawOutput  json.RawMessage    `json:"rawOutput,omitempty"`
	Locations  []ToolCallLocation `json:"locations,omitempty"`
}

// ToolCallContent wraps tool call output content (standard content or diff).
type ToolCallContent struct {
	Meta json.RawMessage `json:"_meta,omitempty"`
	// Standard content fields
	Type string `json:"type"` // "text", "image", "diff", "terminal"
	Text string `json:"text,omitempty"`
	// Diff fields
	Diff *Diff `json:"diff,omitempty"`
	// Terminal fields
	TerminalID string `json:"terminalId,omitempty"`
}

// ToolCallLocation tracks file locations accessed by tools.
type ToolCallLocation struct {
	Meta json.RawMessage `json:"_meta,omitempty"`
	Path string          `json:"path"`
	Line *int            `json:"line,omitempty"`
}

// ---------------------------------------------------------------------------
// Execution plans
// ---------------------------------------------------------------------------

// Plan represents an agent's execution plan.
type Plan struct {
	Meta    json.RawMessage `json:"_meta,omitempty"`
	Entries []PlanEntry     `json:"entries"`
}

// PlanEntry is a single task in the execution plan.
type PlanEntry struct {
	Meta     json.RawMessage   `json:"_meta,omitempty"`
	Content  string            `json:"content"`
	Priority PlanEntryPriority `json:"priority,omitempty"`
	Status   PlanEntryStatus   `json:"status,omitempty"`
}

type PlanEntryStatus string

const (
	PlanEntryStatusPending    PlanEntryStatus = "pending"
	PlanEntryStatusInProgress PlanEntryStatus = "in_progress"
	PlanEntryStatusCompleted  PlanEntryStatus = "completed"
	PlanEntryStatusFailed     PlanEntryStatus = "failed"
)

type PlanEntryPriority string

const (
	PlanEntryPriorityHigh   PlanEntryPriority = "high"
	PlanEntryPriorityMedium PlanEntryPriority = "medium"
	PlanEntryPriorityLow    PlanEntryPriority = "low"
)

// ---------------------------------------------------------------------------
// File diffs
// ---------------------------------------------------------------------------

// Diff represents file modifications.
type Diff struct {
	Meta    json.RawMessage `json:"_meta,omitempty"`
	Path    string          `json:"path"`
	OldText string          `json:"oldText,omitempty"`
	NewText string          `json:"newText,omitempty"`
}

// ---------------------------------------------------------------------------
// Permission requests (Agent → Client)
// ---------------------------------------------------------------------------

// PermissionOptionId uniquely identifies a permission option.
type PermissionOptionId = string

// PermissionOptionKind categorizes the permission option.
type PermissionOptionKind string

const (
	PermissionOptionAllowOnce    PermissionOptionKind = "allow_once"
	PermissionOptionAllowAlways  PermissionOptionKind = "allow_always"
	PermissionOptionRejectOnce   PermissionOptionKind = "reject_once"
	PermissionOptionRejectAlways PermissionOptionKind = "reject_always"
)

// PermissionOption is an option presented to the user.
type PermissionOption struct {
	Meta     json.RawMessage      `json:"_meta,omitempty"`
	OptionID PermissionOptionId   `json:"optionId"`
	Name     string               `json:"name"`
	Kind     PermissionOptionKind `json:"kind,omitempty"`
}

// RequestPermissionRequest asks the client for user authorization.
type RequestPermissionRequest struct {
	Meta      json.RawMessage    `json:"_meta,omitempty"`
	SessionID string             `json:"sessionId"`
	ToolCall  *ToolCallUpdate    `json:"toolCall,omitempty"`
	Options   []PermissionOption `json:"options"`
}

// RequestPermissionResponse is the client's response to a permission request.
type RequestPermissionResponse struct {
	Meta    json.RawMessage          `json:"_meta,omitempty"`
	Outcome RequestPermissionOutcome `json:"outcome"`
}

// RequestPermissionOutcome is a discriminated union.
// ACP encodes the selected variant as {"outcome":"selected","optionId":"..."}
// rather than nesting the selected option in a child object. We keep the
// internal SelectedOption helper but flatten it on the wire, while still
// accepting the legacy nested form during unmarshal for compatibility.
type RequestPermissionOutcome struct {
	Outcome        string                     `json:"outcome"` // "cancelled", "selected", "rejected"
	SelectedOption *SelectedPermissionOutcome `json:"-"`
}

// SelectedPermissionOutcome when user selected an option.
type SelectedPermissionOutcome struct {
	Meta     json.RawMessage    `json:"_meta,omitempty"`
	OptionID PermissionOptionId `json:"optionId"`
}

func (o RequestPermissionOutcome) MarshalJSON() ([]byte, error) {
	type wire struct {
		Outcome  string             `json:"outcome"`
		Meta     json.RawMessage    `json:"_meta,omitempty"`
		OptionID PermissionOptionId `json:"optionId,omitempty"`
	}
	aux := wire{Outcome: o.Outcome}
	if o.SelectedOption != nil {
		aux.Meta = o.SelectedOption.Meta
		aux.OptionID = o.SelectedOption.OptionID
	}
	return json.Marshal(aux)
}

func (o *RequestPermissionOutcome) UnmarshalJSON(data []byte) error {
	type wire struct {
		Outcome        string                     `json:"outcome"`
		Meta           json.RawMessage            `json:"_meta,omitempty"`
		OptionID       PermissionOptionId         `json:"optionId,omitempty"`
		SelectedOption *SelectedPermissionOutcome `json:"selectedOption,omitempty"`
	}
	var aux wire
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	o.Outcome = aux.Outcome
	o.SelectedOption = nil
	if aux.SelectedOption != nil {
		o.SelectedOption = aux.SelectedOption
		return nil
	}
	if aux.Outcome == "selected" && (aux.OptionID != "" || len(aux.Meta) > 0) {
		o.SelectedOption = &SelectedPermissionOutcome{
			Meta:     aux.Meta,
			OptionID: aux.OptionID,
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// File system operations (Agent → Client)
// ---------------------------------------------------------------------------

// ReadTextFileRequest asks the client to read a file.
type ReadTextFileRequest struct {
	Meta json.RawMessage `json:"_meta,omitempty"`
	Path string          `json:"path"`
}

// ReadTextFileResponse returns file contents.
type ReadTextFileResponse struct {
	Meta    json.RawMessage `json:"_meta,omitempty"`
	Content string          `json:"content"`
}

// WriteTextFileRequest asks the client to write a file.
type WriteTextFileRequest struct {
	Meta    json.RawMessage `json:"_meta,omitempty"`
	Path    string          `json:"path"`
	Content string          `json:"content"`
}

// WriteTextFileResponse confirms the write.
type WriteTextFileResponse struct {
	Meta json.RawMessage `json:"_meta,omitempty"`
}

// ---------------------------------------------------------------------------
// Terminal operations (Agent → Client)
// ---------------------------------------------------------------------------

// CreateTerminalRequest asks the client to create a terminal and run a command.
type CreateTerminalRequest struct {
	Meta    json.RawMessage `json:"_meta,omitempty"`
	Command string          `json:"command"`
	Env     []EnvVariable   `json:"env,omitempty"`
	CWD     string          `json:"cwd,omitempty"`
}

// CreateTerminalResponse returns the terminal ID.
type CreateTerminalResponse struct {
	Meta       json.RawMessage `json:"_meta,omitempty"`
	TerminalID string          `json:"terminalId"`
}

// TerminalOutputRequest asks for current terminal output.
type TerminalOutputRequest struct {
	Meta       json.RawMessage `json:"_meta,omitempty"`
	TerminalID string          `json:"terminalId"`
}

// TerminalOutputResponse returns terminal output.
type TerminalOutputResponse struct {
	Meta       json.RawMessage     `json:"_meta,omitempty"`
	Output     string              `json:"output"`
	ExitStatus *TerminalExitStatus `json:"exitStatus,omitempty"`
	Truncated  bool                `json:"truncated,omitempty"`
}

// TerminalExitStatus describes how a terminal command exited.
type TerminalExitStatus struct {
	Meta     json.RawMessage `json:"_meta,omitempty"`
	ExitCode *int            `json:"exitCode,omitempty"`
	Signal   string          `json:"signal,omitempty"`
}

// WaitForTerminalExitRequest waits for terminal command to exit.
type WaitForTerminalExitRequest struct {
	Meta       json.RawMessage `json:"_meta,omitempty"`
	TerminalID string          `json:"terminalId"`
}

// WaitForTerminalExitResponse returns the exit status.
type WaitForTerminalExitResponse struct {
	Meta       json.RawMessage    `json:"_meta,omitempty"`
	ExitStatus TerminalExitStatus `json:"exitStatus"`
}

// KillTerminalRequest kills a terminal without releasing it.
type KillTerminalRequest struct {
	Meta       json.RawMessage `json:"_meta,omitempty"`
	TerminalID string          `json:"terminalId"`
}

// KillTerminalResponse confirms the kill.
type KillTerminalResponse struct {
	Meta json.RawMessage `json:"_meta,omitempty"`
}

// ReleaseTerminalRequest releases terminal resources.
type ReleaseTerminalRequest struct {
	Meta       json.RawMessage `json:"_meta,omitempty"`
	TerminalID string          `json:"terminalId"`
}

// ReleaseTerminalResponse confirms the release.
type ReleaseTerminalResponse struct {
	Meta json.RawMessage `json:"_meta,omitempty"`
}

// ---------------------------------------------------------------------------
// Session notifications (Agent → Client)
// ---------------------------------------------------------------------------

// SessionInfoUpdate notifies about session metadata changes.
type SessionInfoUpdate struct {
	Meta      json.RawMessage `json:"_meta,omitempty"`
	SessionID string          `json:"sessionId"`
	Title     string          `json:"title,omitempty"`
}

// CurrentModeUpdate notifies that the session mode changed.
type CurrentModeUpdate struct {
	Meta      json.RawMessage `json:"_meta,omitempty"`
	SessionID string          `json:"sessionId"`
	Mode      SessionModeId   `json:"mode"`
}

// ConfigOptionUpdate notifies that config options changed.
type ConfigOptionUpdate struct {
	Meta          json.RawMessage       `json:"_meta,omitempty"`
	SessionID     string                `json:"sessionId"`
	ConfigOptions []SessionConfigOption `json:"configOptions"`
}

// AvailableCommandsUpdate notifies about slash command changes.
type AvailableCommandsUpdate struct {
	Meta              json.RawMessage    `json:"_meta,omitempty"`
	SessionID         string             `json:"sessionId"`
	AvailableCommands []AvailableCommand `json:"availableCommands"`
}

// AvailableCommand describes a slash command.
type AvailableCommand struct {
	Meta        json.RawMessage `json:"_meta,omitempty"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Input       interface{}     `json:"input,omitempty"` // AvailableCommandInput
}

// ---------------------------------------------------------------------------
// MCP server configuration
// ---------------------------------------------------------------------------

// MCPServer describes how to connect to an MCP server.
type MCPServer struct {
	Meta     json.RawMessage `json:"_meta,omitempty"`
	Name     string          `json:"name"`
	Type     string          `json:"type,omitempty"` // "http", "sse", "stdio" (default)
	Command  string          `json:"command,omitempty"`
	Args     []string        `json:"args,omitempty"`
	Env      []EnvVariable   `json:"env,omitempty"`
	URL      string          `json:"url,omitempty"`
	Headers  []HTTPHeader    `json:"headers,omitempty"`
	ReadOnly bool            `json:"read_only,omitempty"`
}

type EnvVariable struct {
	Meta  json.RawMessage `json:"_meta,omitempty"`
	Name  string          `json:"name"`
	Value string          `json:"value"`
}

type HTTPHeader struct {
	Meta  json.RawMessage `json:"_meta,omitempty"`
	Name  string          `json:"name"`
	Value string          `json:"value"`
}

// ---------------------------------------------------------------------------
// Annotations
// ---------------------------------------------------------------------------

// Annotations for content blocks.
type Annotations struct {
	Meta         json.RawMessage `json:"_meta,omitempty"`
	Audience     []string        `json:"audience,omitempty"` // "user", "assistant"
	Priority     float64         `json:"priority,omitempty"`
	LastModified string          `json:"lastModified,omitempty"`
}

// ---------------------------------------------------------------------------
// Session load (legacy, still in spec)
// ---------------------------------------------------------------------------

// LoadSessionRequest loads a saved session.
type LoadSessionRequest struct {
	Meta      json.RawMessage `json:"_meta,omitempty"`
	SessionID string          `json:"sessionId"`
}

// LoadSessionResponse returns session data.
type LoadSessionResponse struct {
	Meta      json.RawMessage `json:"_meta,omitempty"`
	SessionID string          `json:"sessionId"`
	Messages  []Message       `json:"messages"`
}

// Message is defined in session.go — do not redeclare here.

// ---------------------------------------------------------------------------
// Extension (forwards compatibility)
// ---------------------------------------------------------------------------

// ExtNotification sends a custom notification not in spec.
type ExtNotification struct {
	Meta   json.RawMessage `json:"_meta,omitempty"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// ExtRequest sends a custom request not in spec.
type ExtRequest struct {
	Meta   json.RawMessage `json:"_meta,omitempty"`
	ID     RequestID       `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// ExtResponse responds to an ExtRequest.
type ExtResponse struct {
	Meta   json.RawMessage `json:"_meta,omitempty"`
	Result interface{}     `json:"result,omitempty"`
}

// ---------------------------------------------------------------------------
// Backward-compatible aliases for handler code
// ---------------------------------------------------------------------------

// Aliases so handler.go/agent_loop.go compile with minimal changes
type (
	// Old type names → new canonical names
	InitializeParams     = InitializeRequest
	InitializeResult     = InitializeResponse
	SessionNewParams     = NewSessionRequest
	SessionNewResult     = NewSessionResponse
	SessionPromptParams  = PromptRequest
	SessionPromptResult  = PromptResponse
	SessionCancelParams  = CancelNotification
	SessionLoadParams    = LoadSessionRequest
	SessionLoadResult    = LoadSessionResponse
	SessionSetModeParams = SetSessionModeRequest
	SessionSetModeResult = SetSessionModeResponse
	AuthenticateParams   = AuthenticateRequest
	AuthenticateResult   = AuthenticateResponse

	// Session update params (old notification wrapper)
	SessionUpdateParams = SessionNotification

	// Permission request/response (old handler code)
	PermissionRequestParams  = RequestPermissionRequest
	PermissionResponseParams = RequestPermissionResponse

	// PermissionRequest is the old name for the tool call being authorized
	PermissionRequest = ToolCallUpdate

	// FS operations
	FSReadTextFileParams  = ReadTextFileRequest
	FSReadTextFileResult  = ReadTextFileResponse
	FSWriteTextFileParams = WriteTextFileRequest
	FSWriteTextFileResult = WriteTextFileResponse

	// Terminal (old types kept for compilation)
	TerminalCreateParams = CreateTerminalRequest
	TerminalCreateResult = CreateTerminalResponse
	TerminalOutputParams = TerminalOutputRequest
)
