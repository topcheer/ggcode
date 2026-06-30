package hooks

import (
	"fmt"
	"time"
)

// HookType determines how a hook is executed.
type HookType string

const (
	HookTypeCommand HookType = "command" // execute a local shell command
	HookTypeHTTP    HookType = "http"    // send an HTTP POST webhook
)

// Hook represents a single hook bound to one event.
type Hook struct {
	Match        string            `yaml:"match" json:"match"`                 // glob/pipe/func-call match pattern. "*" matches all.
	Type         HookType          `yaml:"type" json:"type"`                   // "command" (default) or "http"
	Command      string            `yaml:"command" json:"command"`             // shell command (type=command)
	URL          string            `yaml:"url" json:"url"`                     // webhook URL (type=http)
	Method       string            `yaml:"method" json:"method"`               // HTTP method (type=http), default POST
	Headers      map[string]string `yaml:"headers" json:"headers"`             // custom HTTP headers (type=http)
	Timeout      string            `yaml:"timeout" json:"timeout"`             // timeout duration, e.g. "10s" (type=http). Default 10s.
	Secret       string            `yaml:"secret" json:"secret"`               // HMAC-SHA256 signing key (type=http)
	InjectOutput bool              `yaml:"inject_output" json:"inject_output"` // (post_tool_use only) inject stdout/response body into tool result
}

// HasType returns the effective type, defaulting to command for backward compatibility.
func (h Hook) HasType() HookType {
	if h.Type == HookTypeHTTP {
		return HookTypeHTTP
	}
	return HookTypeCommand
}

// Event names.
const (
	EventOnUserMessage = "on_user_message"
	EventPreToolUse    = "pre_tool_use"
	EventPostToolUse   = "post_tool_use"
	EventOnAgentStop   = "on_agent_stop"
	EventOnStreamStop  = "on_stream_stop"
)

// HookConfig holds all hooks from configuration, keyed by event.
type HookConfig struct {
	OnUserMessage []Hook `yaml:"on_user_message" json:"on_user_message"`
	PreToolUse    []Hook `yaml:"pre_tool_use" json:"pre_tool_use"`
	PostToolUse   []Hook `yaml:"post_tool_use" json:"post_tool_use"`
	OnAgentStop   []Hook `yaml:"on_agent_stop" json:"on_agent_stop"`
	OnStreamStop  []Hook `yaml:"on_stream_stop" json:"on_stream_stop"`
}

// HookResult is the result of running one or more hooks.
type HookResult struct {
	Allowed bool   // false means block the operation (pre hooks only)
	Output  string // captured stdout or HTTP response body (for inject_output)
	Err     error
}

// HookEnv holds all context data passed to hook commands and webhooks.
// It is the Go-native representation of the standardized payload.
type HookEnv struct {
	// Universal
	Event      string // event name (EventPreToolUse, etc.)
	SessionID  string
	Workspace  string
	WorkingDir string

	// Tool context (pre_tool_use, post_tool_use)
	ToolName string
	FilePath string // extracted from tool arguments when applicable
	RawInput string // raw JSON tool arguments
	ToolID   string

	// Tool result (post_tool_use only)
	ToolSuccess  bool
	ToolError    string
	ToolResult   string // truncated tool output (first 4KB)
	ToolDuration string // human-readable duration

	// User message (on_user_message only)
	UserMessage string

	// Stop context (on_agent_stop, on_stream_stop only)
	StopReason string // "completed", "cancelled", "error"
	StopError  string
}

// ValidateHooks checks a HookConfig for common misconfigurations.
// Returns a list of error strings (empty if all valid).
func ValidateHooks(cfg HookConfig) []string {
	var errs []string

	validate := func(event string, hooks []Hook) {
		for i, h := range hooks {
			prefix := fmt.Sprintf("%s[%d]", event, i)

			// HTTP type must have URL
			if h.HasType() == HookTypeHTTP && h.URL == "" {
				errs = append(errs, fmt.Sprintf("%s: type=http requires url", prefix))
			}

			// Command type must have command
			if h.HasType() == HookTypeCommand && h.Command == "" {
				errs = append(errs, fmt.Sprintf("%s: type=command requires command", prefix))
			}

			// Match is required
			if h.Match == "" {
				errs = append(errs, fmt.Sprintf("%s: match is required", prefix))
			}

			// Timeout format check
			if h.Timeout != "" {
				if _, err := time.ParseDuration(h.Timeout); err != nil {
					errs = append(errs, fmt.Sprintf("%s: invalid timeout %q: %v", prefix, h.Timeout, err))
				}
			}

			// inject_output only valid for post_tool_use
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

	return errs
}
