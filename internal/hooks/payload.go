package hooks

import (
	"encoding/json"
	"time"
)

type HookPayload struct {
	Event     string `json:"event"`
	Timestamp string `json:"timestamp"`
	SessionID string `json:"session_id,omitempty"`
	Workspace string `json:"workspace,omitempty"`

	Tool       *PayloadTool       `json:"tool,omitempty"`
	Result     *PayloadResult     `json:"result,omitempty"`
	Msg        *PayloadMsg        `json:"message,omitempty"`
	Stop       *PayloadStop       `json:"stop,omitempty"`
	Compaction *PayloadCompaction `json:"compaction,omitempty"`
	Session    *PayloadSession    `json:"session,omitempty"`
}

type PayloadTool struct {
	Name     string          `json:"name"`
	Input    json.RawMessage `json:"input,omitempty"`
	FilePath string          `json:"file_path,omitempty"`
}

type PayloadResult struct {
	Success    bool   `json:"success"`
	Error      string `json:"error,omitempty"`
	Output     string `json:"output,omitempty"`
	DurationMs int64  `json:"duration_ms,omitempty"`
}

type PayloadMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type PayloadStop struct {
	Reason string `json:"reason"`
	Error  string `json:"error,omitempty"`
}

type PayloadCompaction struct {
	TokenBefore int    `json:"token_before"`
	TokenAfter  int    `json:"token_after"`
	Reclaimed   int    `json:"reclaimed"`
	Trigger     string `json:"trigger,omitempty"`
}

type PayloadSession struct {
	Source string `json:"source,omitempty"`
	Reason string `json:"reason,omitempty"`
}

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

	case EventOnCompaction:
		p.Compaction = &PayloadCompaction{
			TokenBefore: env.TokenBefore,
			TokenAfter:  env.TokenAfter,
			Reclaimed:   env.TokenBefore - env.TokenAfter,
			Trigger:     env.CompactTrigger,
		}

	case EventPreCompact:

		p.Compaction = &PayloadCompaction{
			TokenBefore: env.TokenBefore,
			Trigger:     env.CompactTrigger,
		}

	case EventOnSessionStart:
		p.Session = &PayloadSession{Source: env.SessionSource}

	case EventOnSessionEnd:
		p.Session = &PayloadSession{Reason: env.SessionEndReason}
	}

	return p
}

func (p HookPayload) JSON() []byte {
	b, err := json.Marshal(p)
	if err == nil {
		return b
	}

	sans := p
	if sans.Tool != nil {
		t := *sans.Tool
		t.Input = nil
		sans.Tool = &t
	}
	if b2, err2 := json.Marshal(sans); err2 == nil {
		return b2
	}
	return nil
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
