package a2a

import (
	"encoding/json"
	"fmt"
	"time"
)

// ---------------------------------------------------------------------------
// JSON-RPC 2.0
// ---------------------------------------------------------------------------

// JSONRPCRequest is a generic JSON-RPC 2.0 request.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCResponse is a generic JSON-RPC 2.0 response.
type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

// JSONRPCError follows the JSON-RPC 2.0 error spec.
type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data,omitempty"`
}

func (e *JSONRPCError) Error() string { return fmt.Sprintf("JSON-RPC error %d: %s", e.Code, e.Message) }

// Standard JSON-RPC error codes.
var (
	ErrParseError     = &JSONRPCError{Code: -32700, Message: "Parse error"}
	ErrInvalidRequest = &JSONRPCError{Code: -32600, Message: "Invalid request"}
	ErrMethodNotFound = &JSONRPCError{Code: -32601, Message: "Method not found"}
	ErrInvalidParams  = &JSONRPCError{Code: -32602, Message: "Invalid params"}
	ErrInternal       = &JSONRPCError{Code: -32603, Message: "Internal error"}

	// A2A-specific error codes (per spec, -32000 to -32099).
	ErrTaskNotFound      = &JSONRPCError{Code: -32001, Message: "Task not found"}
	ErrTaskNotCancelable = &JSONRPCError{Code: -32002, Message: "Task not cancelable"}
	ErrPushNotSupported  = &JSONRPCError{Code: -32003, Message: "Push notification not supported"}
	ErrUnsupportedOp     = &JSONRPCError{Code: -32004, Message: "Unsupported operation"}
	ErrContentType       = &JSONRPCError{Code: -32005, Message: "Incompatible content types"}
)

// ---------------------------------------------------------------------------
// Agent Card
// ---------------------------------------------------------------------------

// AgentCard describes an agent's identity, capabilities, and skills.
// Served at GET /.well-known/agent.json
type AgentCard struct {
	Name               string                `json:"name"`
	Description        string                `json:"description"`
	URL                string                `json:"url"`
	Version            string                `json:"version,omitempty"`
	Provider           *AgentProvider        `json:"provider,omitempty"`
	Capabilities       AgentCapabilities     `json:"capabilities"`
	SecuritySchemes    map[string]Security   `json:"securitySchemes,omitempty"`
	Security           []map[string][]string `json:"security,omitempty"`
	DefaultInputModes  []string              `json:"defaultInputModes"`
	DefaultOutputModes []string              `json:"defaultOutputModes"`
	Skills             []Skill               `json:"skills"`
	Metadata           interface{}           `json:"metadata,omitempty"`
}

// AgentProvider identifies the organization behind the agent.
type AgentProvider struct {
	URL          string `json:"url"`
	Organization string `json:"organization"`
}

// AgentCapabilities declares optional protocol features.
type AgentCapabilities struct {
	Streaming         bool `json:"streaming"`
	PushNotifications bool `json:"pushNotifications"`
}

// Skill describes one focused capability of the agent.
type Skill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags,omitempty"`
}

// Security describes a security scheme (API Key only for now).
type Security struct {
	Type        string `json:"type"`               // "apiKey"
	Location    string `json:"location,omitempty"` // "header"
	Name        string `json:"name,omitempty"`     // "X-API-Key"
	Description string `json:"description,omitempty"`
}

// ---------------------------------------------------------------------------
// Task & Messages
// ---------------------------------------------------------------------------

// TaskState represents the lifecycle state of a task.
type TaskState string

const (
	TaskStateSubmitted     TaskState = "submitted"
	TaskStateWorking       TaskState = "working"
	TaskStateInputRequired TaskState = "input-required"
	TaskStateCompleted     TaskState = "completed"
	TaskStateCanceled      TaskState = "canceled"
	TaskStateFailed        TaskState = "failed"
	TaskStateRejected      TaskState = "rejected"
)

// IsTerminal returns true for states that cannot transition further.
func (s TaskState) IsTerminal() bool {
	switch s {
	case TaskStateCompleted, TaskStateFailed, TaskStateCanceled, TaskStateRejected:
		return true
	}
	return false
}

// TaskStatus wraps a TaskState to satisfy the A2A spec requirement that
// task.status is serialized as { "state": "..." } rather than a bare string.
type TaskStatus struct {
	State TaskState `json:"state"`
}

// IsTerminal returns true for states that cannot transition further.
func (s TaskStatus) IsTerminal() bool { return s.State.IsTerminal() }

// Task represents an A2A task with its full lifecycle.
type Task struct {
	ID        string     `json:"id"`
	ContextID string     `json:"contextId"`
	Status    TaskStatus `json:"status"`
	Skill     string     `json:"skill,omitempty"`
	History   []Message  `json:"history,omitempty"`
	Artifacts []Artifact `json:"artifacts,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
}

// Kind returns the A2A object type.
func (t *Task) Kind() string { return "task" }

// Message carries conversation content within a task.
type Message struct {
	Role      string `json:"role"` // "user" or "agent"
	Parts     []Part `json:"parts"`
	MessageID string `json:"messageId,omitempty"`
}

// Part is a discriminated content block (text, file, or data).
type Part struct {
	Kind string          `json:"kind"` // "text", "file", "data"
	Text string          `json:"text,omitempty"`
	File *FilePart       `json:"file,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
}

// FilePart carries a file attachment (inline or by reference).
type FilePart struct {
	Name  string `json:"name,omitempty"`
	MIME  string `json:"mimeType,omitempty"`
	Bytes string `json:"bytes,omitempty"` // base64
	URI   string `json:"uri,omitempty"`   // alternative to bytes
}

// Artifact holds task output.
type Artifact struct {
	ArtifactID string `json:"artifactId"`
	Parts      []Part `json:"parts"`
	Append     bool   `json:"append,omitempty"`
	LastChunk  bool   `json:"lastChunk,omitempty"`
}

// ---------------------------------------------------------------------------
// JSON-RPC method parameter types
// ---------------------------------------------------------------------------

// SendMessageParams is the params for "message/send" and "message/stream".
type SendMessageParams struct {
	Message   Message `json:"message"`
	Skill     string  `json:"skill,omitempty"`
	TaskID    string  `json:"id,omitempty"`        // for continuing an existing task
	ContextID string  `json:"contextId,omitempty"` // for continuing an existing context
}

// GetTaskParams is the params for "tasks/get".
type GetTaskParams struct {
	ID string `json:"id"`
}

// CancelTaskParams is the params for "tasks/cancel".
type CancelTaskParams struct {
	ID string `json:"id"`
}

// TaskSubscriptionParams is the params for "tasks/stream" (SSE subscription).
type TaskSubscriptionParams struct {
	ID string `json:"id"`
}

// TaskEvent represents an SSE event for task updates.
type TaskEvent struct {
	ID    string      `json:"id"`
	Event string      `json:"event"` // "status", "artifact", "completed", "failed"
	Data  interface{} `json:"data"`
}
