package a2a

import (
	"encoding/json"
	"fmt"
	"strings"
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
	// #715: push registration refused because the deployment has no real
	// authentication (no key / only the public default key).
	ErrPushAuthNotConfigured = &JSONRPCError{Code: -32009, Message: "Push notifications disabled: real authentication required"}
	// #1094: dedicated timeout error code (previously collided with TaskNotFound -32001)
	ErrTaskTimeout = &JSONRPCError{Code: -32060, Message: "Task timed out"}
)

// ---------------------------------------------------------------------------
// Agent Card
// ---------------------------------------------------------------------------

// AgentCard describes an agent's identity, capabilities, and skills.
// Served at GET /.well-known/agent.json
type AgentCard struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	URL         string `json:"url"`
	Version     string `json:"version,omitempty"`
	// ProtocolReversion is the A2A protocol version this card speaks (spec
	// field "protocolVersion", required since 0.2.5). Clients use it to
	// select v1.0 media types and encodings.
	ProtocolReversion  string                `json:"protocolVersion,omitempty"`
	Provider           *AgentProvider        `json:"provider,omitempty"`
	Capabilities       AgentCapabilities     `json:"capabilities"`
	SecuritySchemes    map[string]Security   `json:"securitySchemes,omitempty"` // deprecated, kept for compat
	Security           []map[string][]string `json:"security,omitempty"`        // security requirements
	DefaultInputModes  []string              `json:"defaultInputModes"`
	DefaultOutputModes []string              `json:"defaultOutputModes"`
	Skills             []Skill               `json:"skills"`
	Interfaces         []AgentInterface      `json:"interfaces,omitempty"`
	Extensions         []AgentExtension      `json:"extensions,omitempty"`
	Metadata           interface{}           `json:"metadata,omitempty"`
	Lifecycle          *AgentLifecycleInfo   `json:"lifecycle,omitempty"` // AgentHub lifecycle transparency
	// Signatures carries JWS (RFC 7515) signatures over the RFC 8785
	// canonicalized card JSON with the signatures field excluded
	// (A2A spec §8.4). See card_signature.go for verification.
	Signatures []AgentCardSignature `json:"signatures,omitempty"`
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

// DeclaredExtensions returns the extensions this card declares, preferring
// the spec-compliant capabilities location and falling back to the legacy
// top-level field for older ggcode peers.
func (c *AgentCard) DeclaredExtensions() []AgentExtension {
	if c == nil {
		return nil
	}
	if len(c.Capabilities.Extensions) > 0 {
		return c.Capabilities.Extensions
	}
	return c.Extensions
}

// A2AExtensionsHeader is the HTTP header used for extension activation
// (A2A v1.0 "Extension Activation"). The client includes a comma-separated
// list of extension URIs it intends to activate; the server echoes back the
// set that was actually activated.
const A2AExtensionsHeader = "A2A-Extensions"

// ParseA2AExtensionsHeader parses an A2A-Extensions header value into the
// distinct extension URIs it activates. Accepts comma- or space-separated
// lists; empty entries are dropped and duplicates removed (order preserved).
func ParseA2AExtensionsHeader(v string) []string {
	if v == "" {
		return nil
	}
	fields := strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
	seen := make(map[string]struct{}, len(fields))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if _, dup := seen[f]; dup {
			continue
		}
		seen[f] = struct{}{}
		out = append(out, f)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// FormatA2AExtensionsHeader renders extension URIs as a comma-separated
// A2A-Extensions header value.
func FormatA2AExtensionsHeader(uris []string) string {
	cleaned := ParseA2AExtensionsHeader(strings.Join(uris, ","))
	return strings.Join(cleaned, ", ")
}

// AgentProvider identifies the organization behind the agent.
type AgentProvider struct {
	URL          string `json:"url"`
	Organization string `json:"organization"`
}

// AgentLifecycleState represents the lifecycle state of an agent.
// Following AgentHub (ICSE 2026) lifecycle transparency requirements.
type AgentLifecycleState string

const (
	// AgentLifecycleActive is the default state for agents in active development/production use.
	AgentLifecycleActive AgentLifecycleState = "active"
	// AgentLifecycleDeprecated indicates the agent is still functional but should not be used for new deployments.
	AgentLifecycleDeprecated AgentLifecycleState = "deprecated"
	// AgentLifecycleRetired indicates the agent is no longer maintained and may stop working.
	AgentLifecycleRetired AgentLifecycleState = "retired"
	// AgentLifecycleRevoked indicates the agent has been removed due to security or policy violations.
	AgentLifecycleRevoked AgentLifecycleState = "revoked"
)

// IsDeprecatedOrWorse returns true for states that should trigger warnings in discovery and selection.
func (s AgentLifecycleState) IsDeprecatedOrWorse() bool {
	switch s {
	case AgentLifecycleDeprecated, AgentLifecycleRetired, AgentLifecycleRevoked:
		return true
	}
	return false
}

// IsActive returns true only for the active state.
func (s AgentLifecycleState) IsActive() bool {
	return s == AgentLifecycleActive
}

// IsTerminal returns true for states that cannot transition back to active.
func (s AgentLifecycleState) IsTerminal() bool {
	switch s {
	case AgentLifecycleRetired, AgentLifecycleRevoked:
		return true
	}
	return false
}

// AgentCapabilities declares optional protocol features.
type AgentCapabilities struct {
	Streaming         bool `json:"streaming"`
	PushNotifications bool `json:"pushNotifications"`
	ExtendedAgentCard bool `json:"extendedAgentCard,omitempty"`
	// Extensions lists protocol extensions this agent supports (A2A v1.0
	// spec places the declaration inside capabilities; the top-level
	// AgentCard.Extensions field is kept for backwards compatibility with
	// older ggcode peers that only read the flat field).
	Extensions []AgentExtension `json:"extensions,omitempty"`
}

// AgentLifecycleInfo tracks lifecycle state with timestamp and rationale.
type AgentLifecycleInfo struct {
	State     AgentLifecycleState `json:"state"`
	Timestamp time.Time           `json:"timestamp"`
	Rationale string              `json:"rationale,omitempty"` // human-readable reason for state change
}

// Skill describes one focused capability of the agent.
type Skill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags,omitempty"`
	Examples    []string `json:"examples,omitempty"`
}

// ---------------------------------------------------------------------------
// Security Schemes (A2A Spec Section 4.5)
// ---------------------------------------------------------------------------

// SecurityScheme is the OneOf type for all supported authentication schemes.
// Exactly one of the embedded scheme fields should be set.
type SecurityScheme struct {
	// Common fields
	Type        string `json:"type"` // "apiKey", "http", "oauth2", "openIdConnect", "mutualTLS"
	Description string `json:"description,omitempty"`

	// APIKey scheme fields
	In   string `json:"in,omitempty"`   // "header" or "query"
	Name string `json:"name,omitempty"` // header/query parameter name

	// HTTP Auth scheme fields (Bearer, Basic)
	Scheme string `json:"scheme,omitempty"` // "bearer" or "basic"

	// OAuth2 scheme fields
	Flows *OAuthFlows `json:"flows,omitempty"`

	// OpenID Connect scheme fields
	OpenIDConnectURL string `json:"openIdConnectUrl,omitempty"`

	// MutualTLS — no additional fields (cert-based)
}

// OAuthFlows contains the supported OAuth2 grant types.
type OAuthFlows struct {
	AuthorizationCode *OAuthFlowAuthorizationCode `json:"authorizationCode,omitempty"`
	ClientCredentials *OAuthFlowClientCredentials `json:"clientCredentials,omitempty"`
	DeviceCode        *OAuthFlowDeviceCode        `json:"deviceCode,omitempty"`
}

// OAuthFlowAuthorizationCode is the Authorization Code + PKCE flow.
// No client_secret required for public clients.
type OAuthFlowAuthorizationCode struct {
	AuthorizationURL string            `json:"authorizationUrl"`
	TokenURL         string            `json:"tokenUrl"`
	RefreshURL       string            `json:"refreshUrl,omitempty"`
	Scopes           map[string]string `json:"scopes"`
}

// OAuthFlowClientCredentials is the machine-to-machine flow.
// Requires client_id + client_secret (user-configured, not hardcoded).
type OAuthFlowClientCredentials struct {
	TokenURL string            `json:"tokenUrl"`
	Scopes   map[string]string `json:"scopes"`
}

// OAuthFlowDeviceCode is the device authorization flow.
// No client_secret required. Ideal for headless/CLI environments.
type OAuthFlowDeviceCode struct {
	DeviceAuthorizationURL string            `json:"deviceAuthorizationUrl"`
	TokenURL               string            `json:"tokenUrl"`
	RefreshURL             string            `json:"refreshUrl,omitempty"`
	Scopes                 map[string]string `json:"scopes"`
}

// Security describes a security scheme (API Key only for now).
// Deprecated: Use SecurityScheme instead. Kept for backward compat.
type Security struct {
	Type        string `json:"type"`               // "apiKey" or "http"
	Scheme      string `json:"scheme,omitempty"`   // "bearer" when Type == "http" (A2A spec 4.5)
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

// A2A v1.0 (2026-03, ADR-001 ProtoJSON) enum value names. JSON-RPC 0.2.x/0.3.x
// peers serialize states as the legacy lowercase names above; v1.0 transports
// (gRPC / transcoded HTTP) serialize the proto enum value names instead.
// See the 1.0.0 changelog entry "Align enum format with ADR-001 ProtoJSON
// specification" (a2aproject/A2A #1384).
const (
	TaskStateV1Submitted     = "TASK_STATE_SUBMITTED"
	TaskStateV1Working       = "TASK_STATE_WORKING"
	TaskStateV1InputRequired = "TASK_STATE_INPUT_REQUIRED"
	TaskStateV1Completed     = "TASK_STATE_COMPLETED"
	TaskStateV1Canceled      = "TASK_STATE_CANCELED"
	TaskStateV1Failed        = "TASK_STATE_FAILED"
	TaskStateV1Rejected      = "TASK_STATE_REJECTED"
	TaskStateV1AuthRequired  = "TASK_STATE_AUTH_REQUIRED"
)

// taskStateAliases maps every known wire encoding of a task state - legacy
// lowercase, v1.0 ProtoJSON names, the pre-#1283 British spelling, and the
// underscored variants seen from SDK transcoders - to the canonical constant.
var taskStateAliases = map[string]TaskState{
	// legacy (JSON-RPC binding, 0.2.x/0.3.x)
	"submitted":      TaskStateSubmitted,
	"working":        TaskStateWorking,
	"input-required": TaskStateInputRequired,
	"completed":      TaskStateCompleted,
	"canceled":       TaskStateCanceled,
	"failed":         TaskStateFailed,
	"rejected":       TaskStateRejected,
	"auth-required":  TaskStateAuthRequired,
	// historical spellings tolerated for interop
	"cancelled":      TaskStateCanceled,
	"input_required": TaskStateInputRequired,
	"auth_required":  TaskStateAuthRequired,
	// v1.0 ProtoJSON names (ADR-001)
	TaskStateV1Submitted:     TaskStateSubmitted,
	TaskStateV1Working:       TaskStateWorking,
	TaskStateV1InputRequired: TaskStateInputRequired,
	TaskStateV1Completed:     TaskStateCompleted,
	TaskStateV1Canceled:      TaskStateCanceled,
	TaskStateV1Failed:        TaskStateFailed,
	TaskStateV1Rejected:      TaskStateRejected,
	TaskStateV1AuthRequired:  TaskStateAuthRequired,
}

// taskStateV1Names is the canonical → v1.0 ProtoJSON name mapping.
var taskStateV1Names = map[TaskState]string{
	TaskStateSubmitted:     TaskStateV1Submitted,
	TaskStateWorking:       TaskStateV1Working,
	TaskStateInputRequired: TaskStateV1InputRequired,
	TaskStateCompleted:     TaskStateV1Completed,
	TaskStateCanceled:      TaskStateV1Canceled,
	TaskStateFailed:        TaskStateV1Failed,
	TaskStateRejected:      TaskStateV1Rejected,
	TaskStateAuthRequired:  TaskStateV1AuthRequired,
}

// UnmarshalJSON accepts both the legacy lowercase state names and the A2A
// v1.0 ProtoJSON enum names (TASK_STATE_*) on the wire, normalizing to the
// canonical constants. Without this, a v1.0 remote agent reporting
// "TASK_STATE_COMPLETED" decoded as an unknown state whose IsTerminal() is
// false - the caller waited forever on a task that had already finished.
func (s *TaskState) UnmarshalJSON(b []byte) error {
	var raw string
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if mapped, ok := taskStateAliases[raw]; ok {
		*s = mapped
		return nil
	}
	*s = TaskState(raw) // unknown states preserved verbatim (forward compat)
	return nil
}

// MarshalJSON emits the legacy lowercase name so ggcode peers running older
// builds keep round-tripping unchanged (SDK backwards-compat allowance,
// 1.0.0 changelog #1401). Use V1Name when speaking to a v1.0 transport.
func (s TaskState) MarshalJSON() ([]byte, error) { return json.Marshal(string(s)) }

// V1Name returns the A2A v1.0 ProtoJSON enum name for the state
// ("working" → "TASK_STATE_WORKING"); unknown states pass through.
func (s TaskState) V1Name() string {
	if n, ok := taskStateV1Names[s]; ok {
		return n
	}
	return string(s)
}

// IsTerminal returns true for states that cannot transition further.
// #1107: TaskStateAuthRequired is NOT terminal per the A2A spec - listing
// it here froze the task forever (done closed, transitions blocked, cancel
// and continueTask both refused). It is pseudo-terminal like input-required.
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
	// auth-required and input-required are pseudo-terminal (#1107): done is
	// NOT closed for them so the task stays resumable.
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
		// Snapshot the slice header first to avoid concurrent append
		// shrinking the visible length between make and range.
		history := t.History
		cp.History = make([]Message, len(history))
		for i, m := range history {
			cp.History[i] = m.snapshot()
		}
	}
	if t.Artifacts != nil {
		artifacts := t.Artifacts
		cp.Artifacts = make([]Artifact, len(artifacts))
		for i, a := range artifacts {
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
	// Metadata carries protocol extensions from remote peers; it must be
	// deep-copied or every Snapshot-based API path (tasks/get, tasks/list,
	// ActiveTasks) silently drops it (#642).
	if m.Metadata != nil {
		cp.Metadata = make(json.RawMessage, len(m.Metadata))
		copy(cp.Metadata, m.Metadata)
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
	// Same as Message.snapshot: Artifact.Metadata is a protocol-extension
	// transport field and must survive Snapshot (#642).
	if a.Metadata != nil {
		cp.Metadata = make(json.RawMessage, len(a.Metadata))
		copy(cp.Metadata, a.Metadata)
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
// PushNotificationConfig configures HTTP webhook callbacks for task events.
// Health tracking fields (ConsecutiveFailures, NextDeliveryAfter, Disabled) are
// updated by firePushNotifications to implement failure backoff and disable dead endpoints.
type PushNotificationConfig struct {
	TaskID         string              `json:"taskId,omitempty"`
	ID             string              `json:"id"`
	URL            string              `json:"url"`
	Token          string              `json:"token,omitempty"`
	Authentication *AuthenticationInfo `json:"authentication,omitempty"`
	Metadata       json.RawMessage     `json:"metadata,omitempty"`

	// Health tracking fields (not persisted, runtime-only).
	// ConsecutiveFailures counts consecutive delivery failures.
	ConsecutiveFailures int `json:"consecutiveFailures,omitempty"`
	// NextDeliveryAfter is when the next delivery attempt is allowed (exponential backoff).
	NextDeliveryAfter time.Time `json:"nextDeliveryAfter,omitempty"`
	// Disabled indicates the config is permanently disabled due to repeated failures.
	Disabled bool `json:"disabled,omitempty"`
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
	// #1470-B: A2A spec field name for events is taskId (Task itself uses
	// id, which IS spec-conformant) - spec-parsing third-party receivers
	// silently got an empty TaskID from the old tag.
	TaskID string     `json:"taskId"`
	Status TaskStatus `json:"status"`
	Final  bool       `json:"final"`
}

// TaskArtifactUpdateEvent is sent via SSE when an artifact is produced.
type TaskArtifactUpdateEvent struct {
	TaskID    string   `json:"taskId"` // #1470-B: spec field name
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
