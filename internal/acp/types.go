package acp

import "encoding/json"

// JSON-RPC 2.0 基础类型
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"` // always "2.0"
	ID      *int            `json:"id"`      // nil for notifications
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC   string          `json:"jsonrpc"`
	ID        *int            `json:"id"`
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

// ACP Protocol types
type InitializeParams struct {
	ProtocolVersion    int                 `json:"protocolVersion"`
	ClientCapabilities ClientCapabilities  `json:"clientCapabilities"`
	ClientInfo         *ImplementationInfo `json:"clientInfo,omitempty"`
}

type InitializeResult struct {
	ProtocolVersion   int                `json:"protocolVersion"`
	AgentCapabilities AgentCapabilities  `json:"agentCapabilities"`
	AgentInfo         ImplementationInfo `json:"agentInfo"`
	AuthMethods       []AuthMethod       `json:"authMethods"`
}

type ClientCapabilities struct {
	FS       *FSCapability     `json:"fs,omitempty"`
	Terminal bool              `json:"terminal,omitempty"`
	Auth     *AuthCapabilities `json:"auth,omitempty"`
}

type FSCapability struct {
	ReadTextFile  bool `json:"readTextFile"`
	WriteTextFile bool `json:"writeTextFile"`
}

type AuthCapabilities struct {
	Terminal bool `json:"terminal"`
}

type AgentCapabilities struct {
	LoadSession        bool                `json:"loadSession"`
	PromptCapabilities *PromptCapabilities `json:"promptCapabilities,omitempty"`
	MCPCapabilities    *MCPCapabilities    `json:"mcpCapabilities,omitempty"`
}

type PromptCapabilities struct {
	Image           bool `json:"image"`
	Audio           bool `json:"audio"`
	EmbeddedContext bool `json:"embeddedContext"`
}

type MCPCapabilities struct {
	HTTP bool `json:"http"`
	SSE  bool `json:"sse"`
}

type ImplementationInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version"`
}

type AuthMethod struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Type        string            `json:"type,omitempty"` // "agent" | "env_var" | "terminal"
	Vars        []AuthEnvVar      `json:"vars,omitempty"`
	Link        string            `json:"link,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
}

type AuthEnvVar struct {
	Name     string `json:"name"`
	Label    string `json:"label,omitempty"`
	Secret   *bool  `json:"secret,omitempty"`
	Optional *bool  `json:"optional,omitempty"`
}

type SessionNewParams struct {
	CWD        string      `json:"cwd"`
	MCPServers []MCPServer `json:"mcpServers,omitempty"`
}

type SessionNewResult struct {
	SessionID string `json:"sessionId"`
}

type MCPServer struct {
	Name    string        `json:"name"`
	Command string        `json:"command,omitempty"`
	Args    []string      `json:"args,omitempty"`
	Env     []EnvVariable `json:"env,omitempty"`
	Type    string        `json:"type,omitempty"` // "http" | "sse"
	URL     string        `json:"url,omitempty"`
	Headers []HTTPHeader  `json:"headers,omitempty"`
}

type EnvVariable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type HTTPHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type SessionPromptParams struct {
	SessionID string         `json:"sessionId"`
	Prompt    []ContentBlock `json:"prompt"`
}

type SessionPromptResult struct{}

type SessionCancelParams struct {
	SessionID string `json:"sessionId"`
}

type ContentBlock struct {
	Type      string          `json:"type"` // "text", "image", "audio", "resource", "resource_link", "tool_use", "tool_result"
	Text      string          `json:"text,omitempty"`
	ImageURL  string          `json:"imageUrl,omitempty"`
	ImageMIME string          `json:"imageMime,omitempty"`
	ImageData string          `json:"imageData,omitempty"`
	ToolName  string          `json:"toolName,omitempty"`
	ToolID    string          `json:"toolId,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	Output    string          `json:"output,omitempty"`
	IsError   bool            `json:"isError,omitempty"`
}

type SessionUpdateParams struct {
	SessionID string        `json:"sessionId"`
	Update    SessionUpdate `json:"update"`
}

type SessionUpdate struct {
	SessionUpdateType string          `json:"sessionUpdate"` // "agent_message_chunk", "tool_call", "tool_result", "user_message_chunk"
	Content           *ContentBlock   `json:"content,omitempty"`
	ToolCall          *ToolCallUpdate `json:"toolCall,omitempty"`
}

// ToolCallUpdate represents a tool call update per ACP spec.
type ToolCallUpdate struct {
	ToolCallID string        `json:"toolCallId"`
	Title      string        `json:"title,omitempty"`
	Kind       string        `json:"kind,omitempty"`   // "read", "write", "execute"
	Status     string        `json:"status,omitempty"` // "pending", "running", "completed", "failed"
	Content    *ContentBlock `json:"content,omitempty"`
	RawInput   string        `json:"rawInput,omitempty"`
	RawOutput  string        `json:"rawOutput,omitempty"`
}

type PermissionRequestParams struct {
	SessionID string            `json:"sessionId"`
	Request   PermissionRequest `json:"request"`
}

type PermissionRequest struct {
	Type        string `json:"type"` // "fs_write", "fs_read", "terminal"
	Path        string `json:"path,omitempty"`
	Command     string `json:"command,omitempty"`
	Description string `json:"description,omitempty"`
}

// AuthenticateParams for the authenticate method
type AuthenticateParams struct {
	AuthMethodID string `json:"authMethodId"`
}

// AuthenticateResult for the authenticate method response
type AuthenticateResult struct{}

// PermissionResponseParams is the Client's response to a permission request.
type PermissionResponseParams struct {
	SessionID string `json:"sessionId"`
	Approved  bool   `json:"approved"`
}

// FSReadTextFileParams for fs/read_text_file requests (Agent → Client).
type FSReadTextFileParams struct {
	Path string `json:"path"`
}

// FSReadTextFileResult for fs/read_text_file responses (Client → Agent).
type FSReadTextFileResult struct {
	Content string `json:"content"`
}

// FSWriteTextFileParams for fs/write_text_file requests (Agent → Client).
type FSWriteTextFileParams struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// FSWriteTextFileResult for fs/write_text_file responses.
type FSWriteTextFileResult struct{}

// TerminalCreateParams for terminal/create requests (Agent → Client).
type TerminalCreateParams struct {
	Command string            `json:"command"`
	Env     map[string]string `json:"env,omitempty"`
	CWD     string            `json:"cwd,omitempty"`
}

// TerminalCreateResult for terminal/create responses.
type TerminalCreateResult struct {
	TerminalID string `json:"terminalId"`
}

// TerminalOutputParams for terminal/output (Client → Agent notification).
type TerminalOutputParams struct {
	TerminalID string `json:"terminalId"`
	Data       string `json:"data"`
	ExitCode   *int   `json:"exitCode,omitempty"`
}

// SessionLoadParams for the session/load method.
type SessionLoadParams struct {
	SessionID string `json:"sessionId"`
}

// SessionLoadResult for the session/load response.
type SessionLoadResult struct {
	SessionID string    `json:"sessionId"`
	Messages  []Message `json:"messages"`
}

// SessionSetModeParams for the session/set_mode method.
type SessionSetModeParams struct {
	SessionID string `json:"sessionId"`
	Mode      string `json:"mode"` // "supervised", "auto", "bypass", "autopilot"
}

// SessionSetModeResult for the session/set_mode response.
type SessionSetModeResult struct{}
