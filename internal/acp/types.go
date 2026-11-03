package acp

import "encoding/json"

type RequestID = interface{}

type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      RequestID       `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC   string          `json:"jsonrpc"`
	ID        RequestID       `json:"id"`
	Result    interface{}     `json:"result,omitempty"`
	RawResult json.RawMessage `json:"-"`
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

const ProtocolVersion = 1

type InitializeRequest struct {
	Meta               json.RawMessage     `json:"_meta,omitempty"`
	ProtocolVersion    int                 `json:"protocolVersion"`
	ClientCapabilities ClientCapabilities  `json:"clientCapabilities"`
	ClientInfo         *ImplementationInfo `json:"clientInfo,omitempty"`
}

type InitializeResponse struct {
	Meta              json.RawMessage    `json:"_meta,omitempty"`
	ProtocolVersion   int                `json:"protocolVersion"`
	AgentCapabilities AgentCapabilities  `json:"agentCapabilities"`
	AgentInfo         ImplementationInfo `json:"agentInfo"`
	AuthMethods       []AuthMethod       `json:"authMethods"`
}

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

type ImplementationInfo struct {
	Meta    json.RawMessage `json:"_meta,omitempty"`
	Name    string          `json:"name"`
	Title   string          `json:"title,omitempty"`
	Version string          `json:"version"`
}

type AuthMethod struct {
	Meta        json.RawMessage `json:"_meta,omitempty"`
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Type        string          `json:"type,omitempty"`
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

type AuthenticateRequest struct {
	Meta         json.RawMessage `json:"_meta,omitempty"`
	AuthMethodID string          `json:"authMethodId"`
}

type AuthenticateResponse struct {
	Meta json.RawMessage `json:"_meta,omitempty"`
}

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

type NewSessionResponse struct {
	Meta          json.RawMessage       `json:"_meta,omitempty"`
	SessionID     string                `json:"sessionId"`
	Modes         *SessionModeState     `json:"modes,omitempty"`
	ConfigOptions []SessionConfigOption `json:"configOptions,omitempty"`
}

type CloseSessionRequest struct {
	Meta      json.RawMessage `json:"_meta,omitempty"`
	SessionID string          `json:"sessionId"`
}

type CloseSessionResponse struct {
	Meta json.RawMessage `json:"_meta,omitempty"`
}

type ListSessionsRequest struct {
	Meta   json.RawMessage `json:"_meta,omitempty"`
	Cursor string          `json:"cursor,omitempty"`
	CWD    string          `json:"cwd,omitempty"`
}

type ListSessionsResponse struct {
	Meta       json.RawMessage `json:"_meta,omitempty"`
	Sessions   []SessionInfo   `json:"sessions"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

type SessionInfo struct {
	Meta      json.RawMessage `json:"_meta,omitempty"`
	SessionID string          `json:"sessionId"`
	CWD       string          `json:"cwd,omitempty"`
	Title     string          `json:"title,omitempty"`
	CreatedAt string          `json:"createdAt,omitempty"`
	UpdatedAt string          `json:"updatedAt,omitempty"`
}

type ResumeSessionRequest struct {
	Meta       json.RawMessage `json:"_meta,omitempty"`
	SessionID  string          `json:"sessionId"`
	CWD        string          `json:"cwd,omitempty"`
	MCPServers []MCPServer     `json:"mcpServers,omitempty"`
}

type ResumeSessionResponse struct {
	Meta          json.RawMessage       `json:"_meta,omitempty"`
	Modes         *SessionModeState     `json:"modes,omitempty"`
	ConfigOptions []SessionConfigOption `json:"configOptions,omitempty"`
}

type SessionModeId = string

type SessionMode struct {
	Meta        json.RawMessage `json:"_meta,omitempty"`
	ID          SessionModeId   `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
}

type SessionModeState struct {
	Modes   []SessionMode `json:"modes"`
	Current SessionModeId `json:"current"`
}

type SetSessionModeRequest struct {
	Meta      json.RawMessage `json:"_meta,omitempty"`
	SessionID string          `json:"sessionId"`
	Mode      SessionModeId   `json:"mode"`
}

type SetSessionModeResponse struct {
	Meta json.RawMessage `json:"_meta,omitempty"`
}

type SessionConfigId = string

type SessionConfigValueId = string

type SessionConfigGroupId = string

type SessionConfigOption struct {
	Meta         json.RawMessage            `json:"_meta,omitempty"`
	Type         string                     `json:"type"`
	ID           SessionConfigId            `json:"id"`
	Name         string                     `json:"name"`
	Description  string                     `json:"description,omitempty"`
	Category     string                     `json:"category,omitempty"`
	CurrentValue SessionConfigValueId       `json:"currentValue"`
	Options      SessionConfigSelectOptions `json:"options"`
}

type SessionConfigSelectOptions []interface{}

type SessionConfigSelectOption struct {
	Meta    json.RawMessage      `json:"_meta,omitempty"`
	ID      SessionConfigValueId `json:"id"`
	Name    string               `json:"name"`
	GroupID SessionConfigGroupId `json:"groupId,omitempty"`
}

type SessionConfigSelectGroup struct {
	Meta    json.RawMessage             `json:"_meta,omitempty"`
	ID      SessionConfigGroupId        `json:"id"`
	Name    string                      `json:"name"`
	Options []SessionConfigSelectOption `json:"options"`
}

type SetSessionConfigOptionRequest struct {
	Meta      json.RawMessage      `json:"_meta,omitempty"`
	SessionID string               `json:"sessionId"`
	ConfigID  SessionConfigId      `json:"configId"`
	Value     SessionConfigValueId `json:"value"`
}

type SetSessionConfigOptionResponse struct {
	Meta          json.RawMessage       `json:"_meta,omitempty"`
	ConfigOptions []SessionConfigOption `json:"configOptions"`
}

type PromptRequest struct {
	Meta      json.RawMessage `json:"_meta,omitempty"`
	SessionID string          `json:"sessionId"`
	Prompt    []ContentBlock  `json:"prompt"`
}

type PromptResponse struct {
	Meta       json.RawMessage `json:"_meta,omitempty"`
	StopReason StopReason      `json:"stopReason"`
}

type PromptCompleteNotification struct {
	Meta      json.RawMessage `json:"_meta,omitempty"`
	SessionID string          `json:"sessionId"`
	Response  PromptResponse  `json:"response"`
}

type StopReason string

const (
	StopReasonEndTurn   StopReason = "end_turn"
	StopReasonMaxTokens StopReason = "max_tokens"
	StopReasonMaxTurns  StopReason = "max_turns"
	StopReasonCancelled StopReason = "cancelled"
	StopReasonError     StopReason = "error"
	StopReasonToolUse   StopReason = "tool_use"
)

type CancelNotification struct {
	Meta      json.RawMessage `json:"_meta,omitempty"`
	SessionID string          `json:"sessionId"`
}

type ContentBlock struct {
	Type      string            `json:"type"`
	Meta      json.RawMessage   `json:"_meta,omitempty"`
	Text      string            `json:"text,omitempty"`
	ImageURL  string            `json:"imageUrl,omitempty"`
	ImageMIME string            `json:"imageMime,omitempty"`
	ImageData string            `json:"imageData,omitempty"`
	AudioURL  string            `json:"audioUrl,omitempty"`
	AudioMIME string            `json:"audioMime,omitempty"`
	AudioData string            `json:"audioData,omitempty"`
	Resource  *EmbeddedResource `json:"resource,omitempty"`
	URI       string            `json:"uri,omitempty"`
	ToolName  string            `json:"toolName,omitempty"`
	ToolID    string            `json:"toolId,omitempty"`
	Input     json.RawMessage   `json:"input,omitempty"`
	Output    string            `json:"output,omitempty"`
	IsError   bool              `json:"isError,omitempty"`
}

type EmbeddedResource struct {
	Meta json.RawMessage       `json:"_meta,omitempty"`
	Text *TextResourceContents `json:"text,omitempty"`
	Blob *BlobResourceContents `json:"blob,omitempty"`
}

type TextResourceContents struct {
	Meta json.RawMessage `json:"_meta,omitempty"`
	URI  string          `json:"uri"`
	Text string          `json:"text"`
}

type BlobResourceContents struct {
	Meta     json.RawMessage `json:"_meta,omitempty"`
	URI      string          `json:"uri"`
	Blob     string          `json:"blob"`
	MIMEType string          `json:"mimeType,omitempty"`
}

type SessionNotification struct {
	Meta      json.RawMessage `json:"_meta,omitempty"`
	SessionID string          `json:"sessionId"`
	Update    SessionUpdate   `json:"update"`
}

type SessionUpdate struct {
	Type string          `json:"sessionUpdate"`
	Meta json.RawMessage `json:"_meta,omitempty"`

	Content interface{} `json:"content,omitempty"`

	ToolCallID string             `json:"toolCallId,omitempty"`
	Title      string             `json:"title,omitempty"`
	Kind       ToolKind           `json:"kind,omitempty"`
	Status     ToolCallStatus     `json:"status,omitempty"`
	Locations  []ToolCallLocation `json:"locations,omitempty"`
	RawInput   json.RawMessage    `json:"rawInput,omitempty"`
	RawOutput  json.RawMessage    `json:"rawOutput,omitempty"`

	Plan *Plan `json:"plan,omitempty"`

	StopReason StopReason `json:"stopReason,omitempty"`
}

const (
	UpdateAgentMessageChunk = "agent_message_chunk"
	UpdateUserMessageChunk  = "user_message_chunk"
	UpdateToolCall          = "tool_call"
	UpdateToolCallUpdate    = "tool_call_update"
	UpdatePlan              = "plan"
)

type ToolCallContentEntry struct {
	Meta       json.RawMessage `json:"_meta,omitempty"`
	Type       string          `json:"type"`
	Content    *ContentBlock   `json:"content,omitempty"`
	Path       string          `json:"path,omitempty"`
	OldText    string          `json:"oldText,omitempty"`
	NewText    string          `json:"newText,omitempty"`
	TerminalID string          `json:"terminalId,omitempty"`
}

type ToolCallId = string

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

type ToolCallStatus string

const (
	ToolCallStatusPending    ToolCallStatus = "pending"
	ToolCallStatusInProgress ToolCallStatus = "in_progress"
	ToolCallStatusCompleted  ToolCallStatus = "completed"
	ToolCallStatusFailed     ToolCallStatus = "failed"
)

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

type ToolCallContent struct {
	Meta       json.RawMessage `json:"_meta,omitempty"`
	Type       string          `json:"type"`
	Text       string          `json:"text,omitempty"`
	Diff       *Diff           `json:"diff,omitempty"`
	TerminalID string          `json:"terminalId,omitempty"`
}

type ToolCallLocation struct {
	Meta json.RawMessage `json:"_meta,omitempty"`
	Path string          `json:"path"`
	Line *int            `json:"line,omitempty"`
}

type Plan struct {
	Meta    json.RawMessage `json:"_meta,omitempty"`
	Entries []PlanEntry     `json:"entries"`
}

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

type Diff struct {
	Meta    json.RawMessage `json:"_meta,omitempty"`
	Path    string          `json:"path"`
	OldText string          `json:"oldText,omitempty"`
	NewText string          `json:"newText,omitempty"`
}

type PermissionOptionId = string

type PermissionOptionKind string

const (
	PermissionOptionAllowOnce    PermissionOptionKind = "allow_once"
	PermissionOptionAllowAlways  PermissionOptionKind = "allow_always"
	PermissionOptionRejectOnce   PermissionOptionKind = "reject_once"
	PermissionOptionRejectAlways PermissionOptionKind = "reject_always"
)

type PermissionOption struct {
	Meta     json.RawMessage      `json:"_meta,omitempty"`
	OptionID PermissionOptionId   `json:"optionId"`
	Name     string               `json:"name"`
	Kind     PermissionOptionKind `json:"kind,omitempty"`
}

type RequestPermissionRequest struct {
	Meta      json.RawMessage    `json:"_meta,omitempty"`
	SessionID string             `json:"sessionId"`
	ToolCall  *ToolCallUpdate    `json:"toolCall,omitempty"`
	Options   []PermissionOption `json:"options"`
}

type RequestPermissionResponse struct {
	Meta    json.RawMessage          `json:"_meta,omitempty"`
	Outcome RequestPermissionOutcome `json:"outcome"`
}

type RequestPermissionOutcome struct {
	Outcome        string                     `json:"outcome"`
	SelectedOption *SelectedPermissionOutcome `json:"-"`
}

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

type ReadTextFileRequest struct {
	Meta json.RawMessage `json:"_meta,omitempty"`
	Path string          `json:"path"`
}

type ReadTextFileResponse struct {
	Meta    json.RawMessage `json:"_meta,omitempty"`
	Content string          `json:"content"`
}

type WriteTextFileRequest struct {
	Meta    json.RawMessage `json:"_meta,omitempty"`
	Path    string          `json:"path"`
	Content string          `json:"content"`
}

type WriteTextFileResponse struct {
	Meta json.RawMessage `json:"_meta,omitempty"`
}

type CreateTerminalRequest struct {
	Meta    json.RawMessage `json:"_meta,omitempty"`
	Command string          `json:"command"`
	Env     []EnvVariable   `json:"env,omitempty"`
	CWD     string          `json:"cwd,omitempty"`
}

type CreateTerminalResponse struct {
	Meta       json.RawMessage `json:"_meta,omitempty"`
	TerminalID string          `json:"terminalId"`
}

type TerminalOutputRequest struct {
	Meta       json.RawMessage `json:"_meta,omitempty"`
	TerminalID string          `json:"terminalId"`
}

type TerminalOutputResponse struct {
	Meta       json.RawMessage     `json:"_meta,omitempty"`
	Output     string              `json:"output"`
	ExitStatus *TerminalExitStatus `json:"exitStatus,omitempty"`
	Truncated  bool                `json:"truncated,omitempty"`
}

type TerminalExitStatus struct {
	Meta     json.RawMessage `json:"_meta,omitempty"`
	ExitCode *int            `json:"exitCode,omitempty"`
	Signal   string          `json:"signal,omitempty"`
}

type WaitForTerminalExitRequest struct {
	Meta       json.RawMessage `json:"_meta,omitempty"`
	TerminalID string          `json:"terminalId"`
}

type WaitForTerminalExitResponse struct {
	Meta       json.RawMessage    `json:"_meta,omitempty"`
	ExitStatus TerminalExitStatus `json:"exitStatus"`
}

type KillTerminalRequest struct {
	Meta       json.RawMessage `json:"_meta,omitempty"`
	TerminalID string          `json:"terminalId"`
}

type KillTerminalResponse struct {
	Meta json.RawMessage `json:"_meta,omitempty"`
}

type ReleaseTerminalRequest struct {
	Meta       json.RawMessage `json:"_meta,omitempty"`
	TerminalID string          `json:"terminalId"`
}

type ReleaseTerminalResponse struct {
	Meta json.RawMessage `json:"_meta,omitempty"`
}

type SessionInfoUpdate struct {
	Meta      json.RawMessage `json:"_meta,omitempty"`
	SessionID string          `json:"sessionId"`
	Title     string          `json:"title,omitempty"`
}

type CurrentModeUpdate struct {
	Meta      json.RawMessage `json:"_meta,omitempty"`
	SessionID string          `json:"sessionId"`
	Mode      SessionModeId   `json:"mode"`
}

type ConfigOptionUpdate struct {
	Meta          json.RawMessage       `json:"_meta,omitempty"`
	SessionID     string                `json:"sessionId"`
	ConfigOptions []SessionConfigOption `json:"configOptions"`
}

type AvailableCommandsUpdate struct {
	Meta              json.RawMessage    `json:"_meta,omitempty"`
	SessionID         string             `json:"sessionId"`
	AvailableCommands []AvailableCommand `json:"availableCommands"`
}

type AvailableCommand struct {
	Meta        json.RawMessage `json:"_meta,omitempty"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Input       interface{}     `json:"input,omitempty"`
}

type MCPServer struct {
	Meta     json.RawMessage `json:"_meta,omitempty"`
	Name     string          `json:"name"`
	Type     string          `json:"type,omitempty"`
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

type Annotations struct {
	Meta         json.RawMessage `json:"_meta,omitempty"`
	Audience     []string        `json:"audience,omitempty"`
	Priority     float64         `json:"priority,omitempty"`
	LastModified string          `json:"lastModified,omitempty"`
}

type LoadSessionRequest struct {
	Meta      json.RawMessage `json:"_meta,omitempty"`
	SessionID string          `json:"sessionId"`
}

type LoadSessionResponse struct {
	Meta      json.RawMessage `json:"_meta,omitempty"`
	SessionID string          `json:"sessionId"`
	Messages  []Message       `json:"messages"`
}

type ExtNotification struct {
	Meta   json.RawMessage `json:"_meta,omitempty"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type ExtRequest struct {
	Meta   json.RawMessage `json:"_meta,omitempty"`
	ID     RequestID       `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type ExtResponse struct {
	Meta   json.RawMessage `json:"_meta,omitempty"`
	Result interface{}     `json:"result,omitempty"`
}

type (
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

	SessionUpdateParams = SessionNotification

	PermissionRequestParams  = RequestPermissionRequest
	PermissionResponseParams = RequestPermissionResponse

	PermissionRequest = ToolCallUpdate

	FSReadTextFileParams  = ReadTextFileRequest
	FSReadTextFileResult  = ReadTextFileResponse
	FSWriteTextFileParams = WriteTextFileRequest
	FSWriteTextFileResult = WriteTextFileResponse

	TerminalCreateParams = CreateTerminalRequest
	TerminalCreateResult = CreateTerminalResponse
	TerminalOutputParams = TerminalOutputRequest
)
