package hooks

import (
	"context"
	"fmt"
	"regexp"
	"time"
)

type HookType string

const (
	HookTypeCommand HookType = "command"
	HookTypeHTTP    HookType = "http"
)

type Hook struct {
	Match        string            `yaml:"match" json:"match"`
	MatchMode    string            `yaml:"match_mode" json:"match_mode"`
	Type         HookType          `yaml:"type" json:"type"`
	Command      string            `yaml:"command" json:"command"`
	URL          string            `yaml:"url" json:"url"`
	Method       string            `yaml:"method" json:"method"`
	Headers      map[string]string `yaml:"headers" json:"headers"`
	Timeout      string            `yaml:"timeout" json:"timeout"`
	Secret       string            `yaml:"secret" json:"secret"`
	InjectOutput bool              `yaml:"inject_output" json:"inject_output"`
}

func (h Hook) HasType() HookType {
	if h.Type == HookTypeHTTP {
		return HookTypeHTTP
	}
	return HookTypeCommand
}

const (
	EventOnUserMessage = "on_user_message"
	EventPreToolUse    = "pre_tool_use"
	EventPostToolUse   = "post_tool_use"
	EventOnAgentStop   = "on_agent_stop"
	EventOnStreamStop  = "on_stream_stop"
	EventOnCompaction  = "on_compaction"

	EventPreCompact = "pre_compact"

	EventOnSessionStart = "on_session_start"
	EventOnSessionEnd   = "on_session_end"
)

type HookConfig struct {
	OnUserMessage  []Hook `yaml:"on_user_message" json:"on_user_message"`
	PreToolUse     []Hook `yaml:"pre_tool_use" json:"pre_tool_use"`
	PostToolUse    []Hook `yaml:"post_tool_use" json:"post_tool_use"`
	OnAgentStop    []Hook `yaml:"on_agent_stop" json:"on_agent_stop"`
	OnStreamStop   []Hook `yaml:"on_stream_stop" json:"on_stream_stop"`
	OnCompaction   []Hook `yaml:"on_compaction" json:"on_compaction"`
	PreCompact     []Hook `yaml:"pre_compact" json:"pre_compact"`
	OnSessionStart []Hook `yaml:"on_session_start" json:"on_session_start"`
	OnSessionEnd   []Hook `yaml:"on_session_end" json:"on_session_end"`
}

type HookResult struct {
	Allowed bool
	Output  string
	Err     error

	PolicyNotice string
}

type HookEnv struct {
	Event      string
	SessionID  string
	Workspace  string
	WorkingDir string

	ToolName string
	FilePath string
	RawInput string
	ToolID   string

	ToolSuccess  bool
	ToolError    string
	ToolResult   string
	ToolDuration string

	UserMessage string

	StopReason string
	StopError  string

	SessionSource    string
	SessionEndReason string

	TokenBefore int
	TokenAfter  int

	CompactTrigger string

	Ctx context.Context
}

func ValidateHooks(cfg HookConfig) []string {
	var errs []string

	validate := func(event string, hooks []Hook) {
		for i, h := range hooks {
			prefix := fmt.Sprintf("%s[%d]", event, i)

			if h.HasType() == HookTypeHTTP && h.URL == "" {
				errs = append(errs, fmt.Sprintf("%s: type=http requires url", prefix))
			}

			if h.HasType() == HookTypeCommand && h.Command == "" {
				errs = append(errs, fmt.Sprintf("%s: type=command requires command", prefix))
			}

			if h.Match == "" {
				errs = append(errs, fmt.Sprintf("%s: match is required", prefix))
			}

			if h.MatchMode == "regex" {
				if _, err := regexp.Compile(h.Match); err != nil {
					errs = append(errs, fmt.Sprintf("%s: invalid regex: %v", prefix, err))
				}
			}

			if h.Timeout != "" {
				if _, err := time.ParseDuration(h.Timeout); err != nil {
					errs = append(errs, fmt.Sprintf("%s: invalid timeout %q: %v", prefix, h.Timeout, err))
				}
			}

			if h.InjectOutput && event != "post_tool_use" {
				errs = append(errs, fmt.Sprintf("%s: inject_output only valid for post_tool_use", prefix))
			}
		}
	}

	validate("on_user_message", cfg.OnUserMessage)
	validate("pre_tool_use", cfg.PreToolUse)
	validate("post_tool_use", cfg.PostToolUse)
	validate("on_agent_stop", cfg.OnAgentStop)
	validate("on_stream_stop", cfg.OnStreamStop)
	validate("on_compaction", cfg.OnCompaction)
	validate("pre_compact", cfg.PreCompact)
	validate("on_session_start", cfg.OnSessionStart)
	validate("on_session_end", cfg.OnSessionEnd)

	return errs
}

func TestMatch(mode, pattern, toolName, rawInput string) (bool, error) {
	if mode == "regex" {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return false, fmt.Errorf("invalid regex: %w", err)
		}
		combined := toolName
		if rawInput != "" {
			combined += " " + rawInput
		}
		return re.MatchString(combined), nil
	}

	return matchAny(mode, pattern, toolName, rawInput), nil
}
