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
	ErrTaskNotFound              = &JSONRPCError{Code: -32001, Message: "Task not found"}
	ErrTaskNotCancelable         = &JSONRPCError{Code: -32002, Message: "Task not cancelable"}
	ErrPushNotSupported          = &JSONRPCError{Code: -32003, Message: "Push notification not supported"}
	ErrUnsupportedOp             = &JSONRPCError{Code: -32004, Message: "Unsupported operation"}
	ErrContentType               = &JSONRPCError{Code: -32005, Message: "Incompatible content types"}
	ErrAuthRequired              = &JSONRPCError{Code: -32006, Message: "Authentication required"}
	ErrUnsupportedMode           = &JSONRPCError{Code: -32007, Message: "Unsupported output mode"}
	ErrExtendedCardNotConfigured = &JSONRPCError{Code: -32008, Message: "Extended agent card not configured"}
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
	Interfaces         []AgentInterface      `json:"interfaces,omitempty"`
	Extensions         []AgentExtension      `json:"extensions,omitempty"`
	Metadata           interface{}           `json:"metadata,omitempty"`
}

// AgentInterface describes a protocol binding (JSON-RPC, gRPC, REST).
type AgentInterface struct {
	Type     string                 `json:"type"` // "json-rpc-2.0", "grpc", "rest"
	URL      string                 `json:"url"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// AgentExtension declares an optional extension the agent supports.
type AgentExtension struct {
	URI         string                 `json:"uri"`
	Description string                 `json:"description,omitempty"`
	Required    bool                   `json:"required,omitempty"`
	Params      map[string]interface{} `json:"params,omitempty"`
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
	ExtendedAgentCard bool `json:"extendedAgentCard,omitempty"`
}

// Skill describes one focused capability of the agent.
type Skill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags,omitempty"`
	Examples    []string `json:"examples,omitempty"`
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
	TaskStateAuthRequired  TaskState = "auth-required"
)

// IsTerminal returns true for states that cannot transition further.
func (s TaskState) IsTerminal() bool {
	switch s {
	case TaskStateCompleted, TaskStateFailed, TaskStateCanceled, TaskStateRejected, TaskStateAuthRequired:
		return true
	}
	return false
}

// TaskStatus wraps a TaskState to satisfy the A2A spec requirement that
// task.status is serialized as { "state": "..." } rather than a bare string.
type TaskStatus struct {
	State     TaskState `json:"state"`
	Timestamp time.Time `json:"timestamp"`
}

// IsTerminal returns true for states that cannot transition further.
func (s TaskStatus) IsTerminal() bool { return s.State.IsTerminal() }

// Task represents an A2A task with its full lifecycle.
type Task struct {
	ID        string          `json:"id"`
	ContextID string          `json:"contextId"`
	Status    TaskStatus      `json:"status"`
	Skill     string          `json:"skill,omitempty"`
	History   []Message       `json:"history,omitempty"`
	Artifacts []Artifact      `json:"artifacts,omitempty"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
	CreatedAt time.Time       `json:"createdAt"`
	UpdatedAt time.Time       `json:"updatedAt"`

	// done is closed when the task reaches a terminal state (completed, failed, canceled, rejected).
	// Not serialized — used for in-process notification only.
	done chan struct{} `json:"-"`
}

// Kind returns the A2A object type.
func (t *Task) Kind() string { return "task" }

// Snapshot returns a deep copy of the task safe for external consumption.
// The done channel is not copied.
func (t *Task) Snapshot() Task {
	cp := Task{
		ID:        t.ID,
		ContextID: t.ContextID,
		Status:    t.Status,
		Skill:     t.Skill,
		CreatedAt: t.CreatedAt,
		UpdatedAt: t.UpdatedAt,
	}
	if t.Metadata != nil {
		cp.Metadata = make(json.RawMessage, len(t.Metadata))
		copy(cp.Metadata, t.Metadata)
	}
	if t.History != nil {
		cp.History = make([]Message, len(t.History))
		for i, m := range t.History {
			cp.History[i] = m.snapshot()
		}
	}
	if t.Artifacts != nil {
		cp.Artifacts = make([]Artifact, len(t.Artifacts))
		for i, a := range t.Artifacts {
			cp.Artifacts[i] = a.snapshot()
		}
	}
	return cp
}

func (m Message) snapshot() Message {
	cp := Message{
		Role:      m.Role,
		MessageID: m.MessageID,
	}
	if m.Parts != nil {
		cp.Parts = make([]Part, len(m.Parts))
		for i, p := range m.Parts {
			cp.Parts[i] = p.snapshot()
		}
	}
	return cp
}

func (p Part) snapshot() Part {
	cp := Part{
		Kind: p.Kind,
		Text: p.Text,
	}
	if p.File != nil {
		f := *p.File
		cp.File = &f
	}
	if p.Data != nil {
		cp.Data = make(json.RawMessage, len(p.Data))
		copy(cp.Data, p.Data)
	}
	return cp
}

func (a Artifact) snapshot() Artifact {
	cp := Artifact{
		ArtifactID: a.ArtifactID,
		Append:     a.Append,
		LastChunk:  a.LastChunk,
	}
	if a.Parts != nil {
		cp.Parts = make([]Part, len(a.Parts))
		for i, p := range a.Parts {
			cp.Parts[i] = p.snapshot()
		}
	}
	return cp
}

// Message carries conversation content within a task.
type Message struct {
	Role      string          `json:"role"` // "user" or "agent"
	Parts     []Part          `json:"parts"`
	MessageID string          `json:"messageId,omitempty"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
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
	ArtifactID string          `json:"artifactId"`
	Parts      []Part          `json:"parts"`
	Append     bool            `json:"append,omitempty"`
	LastChunk  bool            `json:"lastChunk,omitempty"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
}

// ---------------------------------------------------------------------------
// JSON-RPC method parameter types
// ---------------------------------------------------------------------------

// SendMessageParams is the params for "message/send" and "message/stream".
type SendMessageParams struct {
	Message       Message            `json:"message"`
	Skill         string             `json:"skill,omitempty"`
	TaskID        string             `json:"id,omitempty"`        // for continuing an existing task
	ContextID     string             `json:"contextId,omitempty"` // for continuing an existing context
	Configuration *SendMessageConfig `json:"configuration,omitempty"`
}

// SendMessageConfig controls how the remote agent should handle the message.
type SendMessageConfig struct {
	AcceptedOutputModes []string `json:"acceptedOutputModes,omitempty"`
	HistoryLength       *int     `json:"historyLength,omitempty"`
	PushNotification    string   `json:"pushNotification,omitempty"`
	ReturnImmediately   bool     `json:"returnImmediately,omitempty"`
}

// GetTaskParams is the params for "tasks/get".
type GetTaskParams struct {
	ID            string `json:"id"`
	HistoryLength *int   `json:"historyLength,omitempty"`
}

// CancelTaskParams is the params for "tasks/cancel".
type CancelTaskParams struct {
	ID string `json:"id"`
}

// TaskSubscriptionParams is the params for "tasks/stream" (SSE subscription).
type TaskSubscriptionParams struct {
	ID string `json:"id"`
}

// ---------------------------------------------------------------------------
// Push Notification types
// ---------------------------------------------------------------------------

// PushNotificationConfig describes a callback endpoint for task notifications.
type PushNotificationConfig struct {
	TaskID         string              `json:"taskId,omitempty"`
	ID             string              `json:"id"`
	URL            string              `json:"url"`
	Token          string              `json:"token,omitempty"`
	Authentication *AuthenticationInfo `json:"authentication,omitempty"`
	Metadata       json.RawMessage     `json:"metadata,omitempty"`
}

// AuthenticationInfo carries credentials for push notification callbacks.
type AuthenticationInfo struct {
	Schemes    []string `json:"schemes"`
	Credential string   `json:"credential,omitempty"`
}

// ---------------------------------------------------------------------------
// SSE Stream Response types
// ---------------------------------------------------------------------------

// StreamResponse is the spec OneOf wrapper for streaming and push events.
// Exactly one of Task, Message, StatusUpdate, ArtifactUpdate must be set.
type StreamResponse struct {
	Task           *Task                    `json:"task,omitempty"`
	Message        *Message                 `json:"message,omitempty"`
	StatusUpdate   *TaskStatusUpdateEvent   `json:"statusUpdate,omitempty"`
	ArtifactUpdate *TaskArtifactUpdateEvent `json:"artifactUpdate,omitempty"`
}

// TaskStatusUpdateEvent is sent via SSE when a task's status changes.
type TaskStatusUpdateEvent struct {
	TaskID string     `json:"id"`
	Status TaskStatus `json:"status"`
	Final  bool       `json:"final"`
}

// TaskArtifactUpdateEvent is sent via SSE when an artifact is produced.
type TaskArtifactUpdateEvent struct {
	TaskID    string   `json:"id"`
	Artifact  Artifact `json:"artifact"`
	Append    bool     `json:"append,omitempty"`
	LastChunk bool     `json:"lastChunk,omitempty"`
}

// TaskEvent represents an SSE event for task updates (legacy, kept for compat).
type TaskEvent struct {
	ID    string      `json:"id"`
	Event string      `json:"event"` // "status", "artifact", "completed", "failed"
	Data  interface{} `json:"data"`
}
