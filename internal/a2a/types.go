package a2a

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data,omitempty"`
}

func (e *JSONRPCError) Error() string { return fmt.Sprintf("JSON-RPC error %d: %s", e.Code, e.Message) }

var (
	ErrParseError     = &JSONRPCError{Code: -32700, Message: "Parse error"}
	ErrInvalidRequest = &JSONRPCError{Code: -32600, Message: "Invalid request"}
	ErrMethodNotFound = &JSONRPCError{Code: -32601, Message: "Method not found"}
	ErrInvalidParams  = &JSONRPCError{Code: -32602, Message: "Invalid params"}
	ErrInternal       = &JSONRPCError{Code: -32603, Message: "Internal error"}

	ErrTaskNotFound              = &JSONRPCError{Code: -32001, Message: "Task not found"}
	ErrTaskNotCancelable         = &JSONRPCError{Code: -32002, Message: "Task not cancelable"}
	ErrPushNotSupported          = &JSONRPCError{Code: -32003, Message: "Push notification not supported"}
	ErrUnsupportedOp             = &JSONRPCError{Code: -32004, Message: "Unsupported operation"}
	ErrContentType               = &JSONRPCError{Code: -32005, Message: "Incompatible content types"}
	ErrAuthRequired              = &JSONRPCError{Code: -32006, Message: "Authentication required"}
	ErrUnsupportedMode           = &JSONRPCError{Code: -32007, Message: "Unsupported output mode"}
	ErrExtendedCardNotConfigured = &JSONRPCError{Code: -32008, Message: "Extended agent card not configured"}
	ErrPushAuthNotConfigured     = &JSONRPCError{Code: -32009, Message: "Push notifications disabled: real authentication required"}
	ErrTaskTimeout               = &JSONRPCError{Code: -32060, Message: "Task timed out"}
)

type AgentCard struct {
	Name               string                `json:"name"`
	Description        string                `json:"description"`
	URL                string                `json:"url"`
	Version            string                `json:"version,omitempty"`
	ProtocolReversion  string                `json:"protocolVersion,omitempty"`
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
	Lifecycle          *AgentLifecycleInfo   `json:"lifecycle,omitempty"`
	Signatures         []AgentCardSignature  `json:"signatures,omitempty"`
}

type AgentInterface struct {
	Type     string                 `json:"type"`
	URL      string                 `json:"url"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

type AgentExtension struct {
	URI         string                 `json:"uri"`
	Description string                 `json:"description,omitempty"`
	Required    bool                   `json:"required,omitempty"`
	Params      map[string]interface{} `json:"params,omitempty"`
}

func (c *AgentCard) DeclaredExtensions() []AgentExtension {
	if c == nil {
		return nil
	}
	if len(c.Capabilities.Extensions) > 0 {
		return c.Capabilities.Extensions
	}
	return c.Extensions
}

const A2AExtensionsHeader = "A2A-Extensions"

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

func FormatA2AExtensionsHeader(uris []string) string {
	cleaned := ParseA2AExtensionsHeader(strings.Join(uris, ","))
	return strings.Join(cleaned, ", ")
}

type AgentProvider struct {
	URL          string `json:"url"`
	Organization string `json:"organization"`
}

type AgentLifecycleState string

const (
	AgentLifecycleActive     AgentLifecycleState = "active"
	AgentLifecycleDeprecated AgentLifecycleState = "deprecated"
	AgentLifecycleRetired    AgentLifecycleState = "retired"
	AgentLifecycleRevoked    AgentLifecycleState = "revoked"
)

func (s AgentLifecycleState) IsDeprecatedOrWorse() bool {
	switch s {
	case AgentLifecycleDeprecated, AgentLifecycleRetired, AgentLifecycleRevoked:
		return true
	}
	return false
}

func (s AgentLifecycleState) IsActive() bool {
	return s == AgentLifecycleActive
}

func (s AgentLifecycleState) IsTerminal() bool {
	switch s {
	case AgentLifecycleRetired, AgentLifecycleRevoked:
		return true
	}
	return false
}

type AgentCapabilities struct {
	Streaming         bool             `json:"streaming"`
	PushNotifications bool             `json:"pushNotifications"`
	ExtendedAgentCard bool             `json:"extendedAgentCard,omitempty"`
	Extensions        []AgentExtension `json:"extensions,omitempty"`
	// RuntimeIntrospection advertises the runtime/* namespace (#5575).
	// Optional: absent (false) by default, so off-state cards are unchanged;
	// the card signature scheme is untouched.
	RuntimeIntrospection bool `json:"runtimeIntrospection,omitempty"`
}

type AgentLifecycleInfo struct {
	State     AgentLifecycleState `json:"state"`
	Timestamp time.Time           `json:"timestamp"`
	Rationale string              `json:"rationale,omitempty"`
}

type Skill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags,omitempty"`
	Examples    []string `json:"examples,omitempty"`
	Source      string   `json:"source,omitempty"`
}

type SecurityScheme struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`

	In   string `json:"in,omitempty"`
	Name string `json:"name,omitempty"`

	Scheme string `json:"scheme,omitempty"`

	Flows *OAuthFlows `json:"flows,omitempty"`

	OpenIDConnectURL string `json:"openIdConnectUrl,omitempty"`
}

type OAuthFlows struct {
	AuthorizationCode *OAuthFlowAuthorizationCode `json:"authorizationCode,omitempty"`
	ClientCredentials *OAuthFlowClientCredentials `json:"clientCredentials,omitempty"`
	DeviceCode        *OAuthFlowDeviceCode        `json:"deviceCode,omitempty"`
}

type OAuthFlowAuthorizationCode struct {
	AuthorizationURL string            `json:"authorizationUrl"`
	TokenURL         string            `json:"tokenUrl"`
	RefreshURL       string            `json:"refreshUrl,omitempty"`
	Scopes           map[string]string `json:"scopes"`
}

type OAuthFlowClientCredentials struct {
	TokenURL string            `json:"tokenUrl"`
	Scopes   map[string]string `json:"scopes"`
}

type OAuthFlowDeviceCode struct {
	DeviceAuthorizationURL string            `json:"deviceAuthorizationUrl"`
	TokenURL               string            `json:"tokenUrl"`
	RefreshURL             string            `json:"refreshUrl,omitempty"`
	Scopes                 map[string]string `json:"scopes"`
}

type Security struct {
	Type        string `json:"type"`
	Scheme      string `json:"scheme,omitempty"`
	Location    string `json:"location,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

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

var taskStateAliases = map[string]TaskState{
	"submitted":              TaskStateSubmitted,
	"working":                TaskStateWorking,
	"input-required":         TaskStateInputRequired,
	"completed":              TaskStateCompleted,
	"canceled":               TaskStateCanceled,
	"failed":                 TaskStateFailed,
	"rejected":               TaskStateRejected,
	"auth-required":          TaskStateAuthRequired,
	"cancelled":              TaskStateCanceled,
	"input_required":         TaskStateInputRequired,
	"auth_required":          TaskStateAuthRequired,
	TaskStateV1Submitted:     TaskStateSubmitted,
	TaskStateV1Working:       TaskStateWorking,
	TaskStateV1InputRequired: TaskStateInputRequired,
	TaskStateV1Completed:     TaskStateCompleted,
	TaskStateV1Canceled:      TaskStateCanceled,
	TaskStateV1Failed:        TaskStateFailed,
	TaskStateV1Rejected:      TaskStateRejected,
	TaskStateV1AuthRequired:  TaskStateAuthRequired,
}

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

func (s *TaskState) UnmarshalJSON(b []byte) error {
	var raw string
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if mapped, ok := taskStateAliases[raw]; ok {
		*s = mapped
		return nil
	}
	*s = TaskState(raw)
	return nil
}

func (s TaskState) MarshalJSON() ([]byte, error) { return json.Marshal(string(s)) }

func (s TaskState) V1Name() string {
	if n, ok := taskStateV1Names[s]; ok {
		return n
	}
	return string(s)
}

func (s TaskState) IsTerminal() bool {
	switch s {
	case TaskStateCompleted, TaskStateFailed, TaskStateCanceled, TaskStateRejected:
		return true
	}
	return false
}

type TaskStatus struct {
	State     TaskState `json:"state"`
	Timestamp time.Time `json:"timestamp"`
}

func (s TaskStatus) IsTerminal() bool { return s.State.IsTerminal() }

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

	done chan struct{} `json:"-"`
}

func (t *Task) Kind() string { return "task" }

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

type Message struct {
	Role      string          `json:"role"`
	Parts     []Part          `json:"parts"`
	MessageID string          `json:"messageId,omitempty"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
}

type Part struct {
	Kind string          `json:"kind"`
	Text string          `json:"text,omitempty"`
	File *FilePart       `json:"file,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
}

type FilePart struct {
	Name  string `json:"name,omitempty"`
	MIME  string `json:"mimeType,omitempty"`
	Bytes string `json:"bytes,omitempty"`
	URI   string `json:"uri,omitempty"`
}

type Artifact struct {
	ArtifactID string          `json:"artifactId"`
	Parts      []Part          `json:"parts"`
	Append     bool            `json:"append,omitempty"`
	LastChunk  bool            `json:"lastChunk,omitempty"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
}

type SendMessageParams struct {
	Message       Message            `json:"message"`
	Skill         string             `json:"skill,omitempty"`
	TaskID        string             `json:"id,omitempty"`
	ContextID     string             `json:"contextId,omitempty"`
	Configuration *SendMessageConfig `json:"configuration,omitempty"`
}

type SendMessageConfig struct {
	AcceptedOutputModes []string `json:"acceptedOutputModes,omitempty"`
	HistoryLength       *int     `json:"historyLength,omitempty"`
	PushNotification    string   `json:"pushNotification,omitempty"`
	ReturnImmediately   bool     `json:"returnImmediately,omitempty"`
}

type GetTaskParams struct {
	ID            string `json:"id"`
	HistoryLength *int   `json:"historyLength,omitempty"`
}

type CancelTaskParams struct {
	ID string `json:"id"`
}

type TaskSubscriptionParams struct {
	ID string `json:"id"`
}

type PushNotificationConfig struct {
	TaskID         string              `json:"taskId,omitempty"`
	ID             string              `json:"id"`
	URL            string              `json:"url"`
	Token          string              `json:"token,omitempty"`
	Authentication *AuthenticationInfo `json:"authentication,omitempty"`
	Metadata       json.RawMessage     `json:"metadata,omitempty"`

	ConsecutiveFailures int       `json:"consecutiveFailures,omitempty"`
	NextDeliveryAfter   time.Time `json:"nextDeliveryAfter,omitempty"`
	Disabled            bool      `json:"disabled,omitempty"`
}

type AuthenticationInfo struct {
	Schemes    []string `json:"schemes"`
	Credential string   `json:"credential,omitempty"`
}

type StreamResponse struct {
	Task           *Task                    `json:"task,omitempty"`
	Message        *Message                 `json:"message,omitempty"`
	StatusUpdate   *TaskStatusUpdateEvent   `json:"statusUpdate,omitempty"`
	ArtifactUpdate *TaskArtifactUpdateEvent `json:"artifactUpdate,omitempty"`
}

type TaskStatusUpdateEvent struct {
	TaskID string     `json:"taskId"`
	Status TaskStatus `json:"status"`
	Final  bool       `json:"final"`
}

type TaskArtifactUpdateEvent struct {
	TaskID    string   `json:"taskId"`
	Artifact  Artifact `json:"artifact"`
	Append    bool     `json:"append,omitempty"`
	LastChunk bool     `json:"lastChunk,omitempty"`
}

type TaskEvent struct {
	ID    string      `json:"id"`
	Event string      `json:"event"`
	Data  interface{} `json:"data"`
}
