package hooks

import (
	"encoding/json"
	"time"
)

// HookPayload is the standardized JSON payload sent to all hooks.
// For command hooks: serialized to GGCODE_HOOK_PAYLOAD env var + stdin.
// For http hooks: sent as the POST request body.
type HookPayload struct {
	Event     string `json:"event"`     // event name
	Timestamp string `json:"timestamp"` // RFC3339
	SessionID string `json:"session_id,omitempty"`
	Workspace string `json:"workspace,omitempty"`

	Tool   *PayloadTool   `json:"tool,omitempty"`    // pre/post_tool_use
	Result *PayloadResult `json:"result,omitempty"`  // post_tool_use only
	Msg    *PayloadMsg    `json:"message,omitempty"` // on_user_message only
	Stop   *PayloadStop   `json:"stop,omitempty"`    // on_agent_stop / on_stream_stop
}

type PayloadTool struct {
	Name     string          `json:"name"`
	Input    json.RawMessage `json:"input,omitempty"`     // raw JSON tool arguments
	FilePath string          `json:"file_path,omitempty"` // convenience: extracted path
}

type PayloadResult struct {
	Success    bool   `json:"success"`
	Error      string `json:"error,omitempty"`
	Output     string `json:"output,omitempty"` // truncated to 4KB
	DurationMs int64  `json:"duration_ms,omitempty"`
}

type PayloadMsg struct {
	Role    string `json:"role"`    // always "user"
	Content string `json:"content"` // text content
}

type PayloadStop struct {
	Reason string `json:"reason"` // "completed", "cancelled", "error"
	Error  string `json:"error,omitempty"`
}

// BuildPayload constructs the standardized payload from HookEnv.
func BuildPayload(env HookEnv) HookPayload {
	p := HookPayload{
		Event:     env.Event,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		SessionID: env.SessionID,
		Workspace: env.Workspace,
	}

	switch env.Event {
	case EventOnUserMessage:
		p.Msg = &PayloadMsg{Role: "user", Content: env.UserMessage}

	case EventPreToolUse:
		p.Tool = &PayloadTool{
			Name:     env.ToolName,
			Input:    json.RawMessage(env.RawInput),
			FilePath: env.FilePath,
		}

	case EventPostToolUse:
		p.Tool = &PayloadTool{
			Name:     env.ToolName,
			Input:    json.RawMessage(env.RawInput),
			FilePath: env.FilePath,
		}
		p.Result = &PayloadResult{
			Success:    env.ToolSuccess,
			Error:      env.ToolError,
			Output:     env.ToolResult,
			DurationMs: parseDurationMs(env.ToolDuration),
		}

	case EventOnAgentStop, EventOnStreamStop:
		p.Stop = &PayloadStop{
			Reason: env.StopReason,
			Error:  env.StopError,
		}
	}

	return p
}

// JSON serializes the payload to JSON bytes.
func (p HookPayload) JSON() []byte {
	b, _ := json.Marshal(p)
	return b
}

func parseDurationMs(s string) int64 {
	if s == "" {
		return 0
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0
	}
	return d.Milliseconds()
}
