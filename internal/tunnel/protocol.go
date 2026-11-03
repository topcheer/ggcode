package tunnel

import "strings"

import "encoding/json"

type GatewayMessage struct {
	SessionID      string          `json:"session_id,omitempty"`
	EventID        string          `json:"event_id,omitempty"`
	StreamID       string          `json:"stream_id,omitempty"`
	MessageID      string          `json:"message_id,omitempty"`
	AuthorityEpoch uint64          `json:"authority_epoch,omitempty"`
	Type           string          `json:"type"`
	Data           json.RawMessage `json:"data,omitempty"`
}

const (
	EventConnected             = "connected"
	EventActiveSession         = "active_session"
	EventServerReady           = "server_ready"
	EventSessionInfo           = "session_info"
	EventActivity              = "activity"
	EventUserMessage           = "user_message"
	EventSystemMessage         = "system_message"
	EventText                  = "text"
	EventTextDone              = "text_done"
	EventReasoning             = "reasoning"
	EventReasoningDone         = "reasoning_done"
	EventStatus                = "status"
	EventToolCall              = "tool_call"
	EventToolResult            = "tool_result"
	EventApprovalRequest       = "approval_request"
	EventApprovalResult        = "approval_result"
	EventAskUserRequest        = "ask_user_request"
	EventAskUserResponse       = "ask_user_response"
	EventSubagentSpawn         = "subagent_spawn"
	EventSubagentText          = "subagent_text"
	EventSubagentReasoning     = "subagent_reasoning"
	EventSubagentReasoningDone = "subagent_reasoning_done"
	EventSubagentStatus        = "subagent_status"
	EventSubagentToolCall      = "subagent_tool_call"
	EventSubagentToolResult    = "subagent_tool_result"
	EventSubagentComplete      = "subagent_complete"
	EventResumeMiss            = "resume_miss"
	EventSnapshotReset         = "snapshot_reset"
	EventReplayDone            = "replay_done"
	EventLanguageChange        = "language_change"
	EventError                 = "error"
	EventPing                  = "ping"
	EventDisconnected          = "disconnected"
	EventServerOffline         = "server_offline"
	EventSharingStopped        = "sharing_stopped"
	EventRelayAck              = "relay_ack"
	EventServerAck             = "server_ack"

	EventFileOffer = "file_offer"
	EventFileChunk = "file_chunk"
	EventFileDone  = "file_done"

	EventRTCOffer     = "rtc_offer"
	EventRTCAnswer    = "rtc_answer"
	EventRTCCandidate = "rtc_candidate"
	EventRTCConnected = "rtc_connected"
	EventRTCFailed    = "rtc_failed"
)

const ActiveSessionModeReplaceHistory = "replace_history"

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

const (
	StatusIdle     = "idle"
	StatusBusy     = "busy"
	StatusThinking = "thinking"
	StatusRunning  = "running"
	StatusWaiting  = "waiting"
	StatusError    = "error"
)

const (
	ModeSupervised = "supervised"
	ModeAuto       = "auto"
	ModeBypass     = "bypass"
	ModeAutopilot  = "autopilot"
)

const (
	DecisionAllow       = "allow"
	DecisionDeny        = "deny"
	DecisionAlwaysAllow = "always_allow"
)

const (
	ResumeModeIncremental      = "incremental"
	ResumeModeFullHistory      = "full_history"
	ResumeModeSnapshotRequired = "snapshot_required"
)

type SessionInfoData struct {
	Workspace string `json:"workspace"`
	Model     string `json:"model"`
	Provider  string `json:"provider"`
	Mode      string `json:"mode"`
	Version   string `json:"version"`
	Title     string `json:"title,omitempty"`
	Language  string `json:"language,omitempty"`
	Theme     string `json:"theme,omitempty"`
}

type ActiveSessionData struct {
	SessionID      string `json:"session_id"`
	BarrierEventID string `json:"barrier_event_id,omitempty"`
	BarrierOrdinal int64  `json:"barrier_ordinal,omitempty"`
	ProjectionHash string `json:"projection_hash,omitempty"`
	WorkspacePath  string `json:"workspace_path,omitempty"`
	ProviderName   string `json:"provider_name,omitempty"`
	ModelName      string `json:"model_name,omitempty"`
}

type LanguageChangeData struct {
	Language string `json:"language"`
}

type ThemeChangeData struct {
	Theme string `json:"theme"`
}

const EventThemeChange = "theme_change"

const CmdThemeChange = "theme_change"

type ResumeHelloData struct {
	ClientID    string `json:"client_id"`
	SessionID   string `json:"session_id,omitempty"`
	LastEventID string `json:"last_event_id,omitempty"`
}

type ResumeFromData struct {
	ClientID    string `json:"client_id"`
	SessionID   string `json:"session_id,omitempty"`
	LastEventID string `json:"last_event_id,omitempty"`
}

type ResumeAckData struct {
	ClientID   string `json:"client_id"`
	SessionID  string `json:"session_id,omitempty"`
	ResumeMode string `json:"resume_mode"`
}

type TextData struct {
	ID    string `json:"id"`
	Chunk string `json:"chunk"`
	Done  bool   `json:"done"`
	Kind  string `json:"kind,omitempty"`
}

const (
	MessageKindCron         = "cron"
	MessageKindShellCommand = "shell_command"
	MessageKindShellOutput  = "shell_output"
)

type StatusData struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type ActivityData struct {
	Activity string `json:"activity,omitempty"`
}

type ToolCallData struct {
	ToolID      string `json:"tool_id"`
	ToolName    string `json:"tool_name"`
	DisplayName string `json:"display_name,omitempty"`
	Args        string `json:"args,omitempty"`
	Detail      string `json:"detail,omitempty"`
}

type ToolResultData struct {
	ToolID      string `json:"tool_id"`
	ToolName    string `json:"tool_name"`
	Result      string `json:"result,omitempty"`
	Summary     string `json:"summary,omitempty"`
	Payload     string `json:"payload,omitempty"`
	PayloadMode string `json:"payload_mode,omitempty"`
	IsError     bool   `json:"is_error,omitempty"`
}

type ApprovalRequestData struct {
	ID       string `json:"id"`
	ToolName string `json:"tool_name"`
	Input    string `json:"input"`
}

type ApprovalResponseData struct {
	ID       string `json:"id"`
	Decision string `json:"decision"`
}

type ImageData struct {
	MIME string `json:"mime"`
	Data string `json:"data"`
	Name string `json:"name,omitempty"`
}

type MessageData struct {
	Text        string      `json:"text"`
	DisplayText string      `json:"display_text,omitempty"`
	Kind        string      `json:"kind,omitempty"`
	Images      []ImageData `json:"images,omitempty"`
	MessageID   string      `json:"message_id,omitempty"`
}

func NormalizeClientMessageID(messageID string) string {
	messageID = strings.TrimSpace(messageID)
	if !strings.HasPrefix(messageID, "user-") {
		return ""
	}
	return messageID
}

type AckData struct {
	MessageID string `json:"message_id"`
}

type ModeChangeData struct {
	Mode string `json:"mode"`
}

type ErrorData struct {
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
}

type AskUserRequestData struct {
	ID        string            `json:"id"`
	Title     string            `json:"title,omitempty"`
	Questions []AskUserQuestion `json:"questions"`
}

type AskUserQuestion struct {
	ID            string          `json:"id"`
	Prompt        string          `json:"prompt"`
	Kind          string          `json:"kind"`
	Choices       []AskUserChoice `json:"choices,omitempty"`
	AllowFreeform bool            `json:"allow_freeform,omitempty"`
	Placeholder   string          `json:"placeholder,omitempty"`
}

type AskUserChoice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type AskUserResponseData struct {
	ID      string          `json:"id"`
	Status  string          `json:"status"`
	Answers []AskUserAnswer `json:"answers,omitempty"`
}

type AskUserAnswer struct {
	QuestionID   string   `json:"question_id"`
	ChoiceIDs    []string `json:"choice_ids,omitempty"`
	FreeformText string   `json:"freeform_text,omitempty"`
}

type SubagentSpawnData struct {
	AgentID  string `json:"agent_id"`
	Name     string `json:"name"`
	Task     string `json:"task"`
	Color    string `json:"color,omitempty"`
	ParentID string `json:"parent_id,omitempty"`
}

type SubagentTextData struct {
	AgentID string `json:"agent_id"`
	ID      string `json:"id"`
	Chunk   string `json:"chunk"`
	Done    bool   `json:"done"`
}

type SubagentReasoningData struct {
	AgentID string `json:"agent_id"`
	ID      string `json:"id"`
	Chunk   string `json:"chunk"`
	Done    bool   `json:"done"`
}

type SubagentStatusData struct {
	AgentID string `json:"agent_id"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type SubagentToolCallData struct {
	AgentID     string `json:"agent_id"`
	ToolID      string `json:"tool_id"`
	ToolName    string `json:"tool_name"`
	DisplayName string `json:"display_name,omitempty"`
	Args        string `json:"args,omitempty"`
	Detail      string `json:"detail,omitempty"`
}

type SubagentToolResultData struct {
	AgentID     string `json:"agent_id"`
	ToolID      string `json:"tool_id"`
	ToolName    string `json:"tool_name"`
	DisplayName string `json:"display_name,omitempty"`
	Detail      string `json:"detail,omitempty"`
	Result      string `json:"result"`
	Summary     string `json:"summary,omitempty"`
	Payload     string `json:"payload,omitempty"`
	PayloadMode string `json:"payload_mode,omitempty"`
	IsError     bool   `json:"is_error"`
}

type SubagentCompleteData struct {
	AgentID string `json:"agent_id"`
	Name    string `json:"name"`
	Summary string `json:"summary"`
	Success bool   `json:"success"`
}

type RTCOfferData struct {
	SDP  string `json:"sdp"`
	Type string `json:"type"`
}

type RTCAnswerData struct {
	SDP  string `json:"sdp"`
	Type string `json:"type"`
}

type RTCCandidateData struct {
	Candidate     string  `json:"candidate"`
	SDPMid        string  `json:"sdp_mid,omitempty"`
	SDPMLineIndex *uint16 `json:"sdp_mline_index,omitempty"`
}
