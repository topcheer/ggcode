package hooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/util"
)

// Dispatch runs hooks for a given event. It is the single entry point for all
// hook execution.
//
// For blocking events (on_user_message, pre_tool_use): runs synchronously.
// Returns HookResult with Allowed=false if any hook blocks.
//
// For non-blocking events (post_tool_use, on_agent_stop, on_stream_stop):
// post_tool_use runs synchronously (to allow inject_output).
// on_agent_stop and on_stream_stop run asynchronously (fire-and-forget).
func Dispatch(cfg HookConfig, env HookEnv) HookResult {
	var hooks []Hook
	switch env.Event {
	case EventOnUserMessage:
		hooks = cfg.OnUserMessage
	case EventPreToolUse:
		hooks = cfg.PreToolUse
	case EventPostToolUse:
		hooks = cfg.PostToolUse
	case EventOnAgentStop:
		hooks = cfg.OnAgentStop
	case EventOnStreamStop:
		hooks = cfg.OnStreamStop
	default:
		return HookResult{Allowed: true}
	}

	// Async fire-and-forget for stop events.
	if env.Event == EventOnAgentStop || env.Event == EventOnStreamStop {
		payload := BuildPayload(env)
		for _, h := range hooks {
			if !matchAny(h.MatchMode, h.Match, env.ToolName, env.RawInput) {
				continue
			}
			h := h // capture for goroutine
			safego.Go("hooks.async."+env.Event, func() {
				_ = executeHook(h, env, payload)
			})
		}
		return HookResult{Allowed: true}
	}

	// Sync execution for on_user_message, pre_tool_use, post_tool_use.
	return runSync(hooks, env)
}

// runSync runs hooks sequentially. For blocking events, the first block wins.
// For post_tool_use, collects inject_output from all matching hooks.
func runSync(hooksList []Hook, env HookEnv) HookResult {
	payload := BuildPayload(env)
	var injectedOutput strings.Builder

	for _, h := range hooksList {
		if !matchAny(h.MatchMode, h.Match, env.ToolName, env.RawInput) {
			continue
		}
		result := executeHook(h, env, payload)
		if !result.Allowed {
			return result
		}
		if env.Event == EventPostToolUse && h.InjectOutput && result.Output != "" {
			injectedOutput.WriteString(result.Output)
			if !strings.HasSuffix(result.Output, "\n") {
				injectedOutput.WriteString("\n")
			}
		}
	}

	return HookResult{Allowed: true, Output: injectedOutput.String()}
}

// executeHook dispatches to command or http execution based on hook type.
func executeHook(h Hook, env HookEnv, payload HookPayload) HookResult {
	switch h.HasType() {
	case HookTypeHTTP:
		return executeHTTPHook(h, env, payload)
	default:
		return executeCommandHook(h, env, payload)
	}
}

// executeCommandHook runs a local shell command.
// Exit code 2 = block (pre hooks). Stdout captured for inject_output.
func executeCommandHook(h Hook, env HookEnv, payload HookPayload) HookResult {
	payloadJSON := string(payload.JSON())

	debug.Log("hooks", "%s: type=command tool=%s match=%s", env.Event, env.ToolName, h.Match)

	// Template expansion — only known vars, preserve unknown for shell.
	expanded := os.Expand(h.Command, func(key string) string {
		switch key {
		case "TOOL_NAME":
			return env.ToolName
		case "FILE_PATH":
			return env.FilePath
		case "WORKING_DIR":
			return env.WorkingDir
		case "RAW_INPUT":
			return env.RawInput
		case "TOOL_SUCCESS":
			return strconv.FormatBool(env.ToolSuccess)
		case "TOOL_ERROR":
			return env.ToolError
		case "TOOL_RESULT":
			return env.ToolResult
		case "TOOL_DURATION":
			return env.ToolDuration
		case "EVENT":
			return env.Event
		case "PAYLOAD":
			return payloadJSON
		default:
			return "${" + key + "}"
		}
	})

	c, _, err := util.NewShellCommand(expanded)
	if err != nil {
		return HookResult{Allowed: true, Err: fmt.Errorf("resolve hook shell: %w", err)}
	}
	c.Dir = env.WorkingDir
	c.Env = append(os.Environ(),
		"GGCODE_HOOK_PAYLOAD="+payloadJSON,
		"GGCODE_HOOK_EVENT="+env.Event,
		"GGCODE_RAW_INPUT="+env.RawInput,
		"GGCODE_TOOL_NAME="+env.ToolName,
		"GGCODE_TOOL_SUCCESS="+strconv.FormatBool(env.ToolSuccess),
		"GGCODE_TOOL_ERROR="+env.ToolError,
		"GGCODE_TOOL_RESULT="+env.ToolResult,
		"GGCODE_TOOL_DURATION="+env.ToolDuration,
	)
	// Also pipe payload to stdin.
	c.Stdin = strings.NewReader(payloadJSON)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr

	err = c.Run()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 2 {
			blockMsg := strings.TrimSpace(stderr.String())
			if blockMsg == "" {
				blockMsg = strings.TrimSpace(stdout.String())
			}
			debug.Log("hooks", "%s BLOCKED: tool=%s reason=%s", env.Event, env.ToolName, blockMsg)
			return HookResult{
				Allowed: false,
				Output:  fmt.Sprintf("Blocked by %s hook: %s", env.Event, blockMsg),
				Err:     err,
			}
		}
		return HookResult{Allowed: true, Output: stdout.String(), Err: fmt.Errorf("hook command failed: %w", err)}
	}

	return HookResult{Allowed: true, Output: stdout.String()}
}

// executeHTTPHook sends an HTTP POST with the standardized payload.
// HTTP 403 = block (pre hooks). Response body captured for inject_output.
func executeHTTPHook(h Hook, env HookEnv, payload HookPayload) HookResult {
	payloadJSON := payload.JSON()

	debug.Log("hooks", "%s: type=http tool=%s url=%s", env.Event, env.ToolName, h.URL)

	timeout := 10 * time.Second
	if h.Timeout != "" {
		if d, err := time.ParseDuration(h.Timeout); err == nil {
			timeout = d
		}
	}

	method := h.Method
	if method == "" {
		method = "POST"
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, method, h.URL, bytes.NewReader(payloadJSON))
	if err != nil {
		return HookResult{Allowed: true, Err: fmt.Errorf("create hook request: %w", err)}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GGCode-Event", env.Event)
	for k, v := range h.Headers {
		req.Header.Set(k, v)
	}

	// HMAC signature for receivers to verify authenticity.
	if h.Secret != "" {
		mac := hmac.New(sha256.New, []byte(h.Secret))
		mac.Write(payloadJSON)
		sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		req.Header.Set("X-GGCode-Signature", sig)
	}

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		debug.Log("hooks", "%s HTTP ERROR: %v", env.Event, err)
		return HookResult{Allowed: true, Err: fmt.Errorf("hook HTTP request: %w", err)}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024)) // cap at 64KB

	if resp.StatusCode == http.StatusForbidden {
		blockMsg := strings.TrimSpace(string(body))
		debug.Log("hooks", "%s BLOCKED: HTTP %d body=%s", env.Event, resp.StatusCode, blockMsg)
		return HookResult{
			Allowed: false,
			Output:  fmt.Sprintf("Blocked by %s hook: %s", env.Event, blockMsg),
		}
	}

	if resp.StatusCode >= 400 {
		debug.Log("hooks", "%s HTTP %d (non-blocking)", env.Event, resp.StatusCode)
		return HookResult{Allowed: true, Err: fmt.Errorf("hook HTTP %d", resp.StatusCode)}
	}

	return HookResult{Allowed: true, Output: string(body)}
}

// --- Legacy dispatchers (backward compatibility) ---

// RunPreHooks runs pre_tool_use hooks synchronously. Blocking: exit 2 / HTTP 403.
func RunPreHooks(hooks []Hook, env HookEnv) HookResult {
	env.Event = EventPreToolUse
	return runSync(hooks, env)
}

// RunPostHooks runs post_tool_use hooks synchronously. Non-blocking; collects inject_output.
func RunPostHooks(hooks []Hook, env HookEnv) HookResult {
	env.Event = EventPostToolUse
	return runSync(hooks, env)
}

// RunUserMessageHooks runs on_user_message hooks synchronously. Blocking.
func RunUserMessageHooks(hooks []Hook, env HookEnv) HookResult {
	env.Event = EventOnUserMessage
	return runSync(hooks, env)
}

// RunAgentStopHooks runs on_agent_stop hooks asynchronously (fire-and-forget).
func RunAgentStopHooks(cfg HookConfig, env HookEnv) {
	env.Event = EventOnAgentStop
	Dispatch(cfg, env)
}

// RunStreamStopHooks runs on_stream_stop hooks asynchronously (fire-and-forget).
func RunStreamStopHooks(cfg HookConfig, env HookEnv) {
	env.Event = EventOnStreamStop
	Dispatch(cfg, env)
}

// --- Matching ---

// matchAny checks if a hook's match pattern applies.
// For non-tool events (on_user_message, on_agent_stop, on_stream_stop),
// toolName is empty and only "*" / "" patterns match.
func matchAny(mode, pattern, toolName, rawInput string) bool {
	if mode == "regex" {
		combined := toolName
		if rawInput != "" {
			combined += " " + rawInput
		}
		matched, err := regexp.MatchString(pattern, combined)
		if err != nil {
			return false
		}
		return matched
	}
	if pattern == "" || pattern == "*" {
		return true
	}
	return matchTool(pattern, toolName, rawInput)
}

// matchTool checks if a hook's match pattern applies to a tool call.
func matchTool(pattern, toolName, rawInput string) bool {
	// Function call pattern: tool_name(args...)
	if parenIdx := strings.Index(pattern, "("); parenIdx > 0 {
		// Guard against patterns like "edit_file(" — missing closing paren.
		if parenIdx+1 > len(pattern)-1 {
			return false
		}
		patTool := pattern[:parenIdx]
		patArgs := pattern[parenIdx+1 : len(pattern)-1]

		if patTool != toolName {
			return false
		}
		if patArgs == "*" || patArgs == "" {
			return true
		}
		if strings.HasSuffix(patArgs, "*") {
			prefix := strings.TrimSuffix(patArgs, "*")
			return strings.Contains(rawInput, prefix)
		}
		return strings.Contains(rawInput, patArgs)
	}

	// Simple glob match on tool name
	matched, _ := filepath.Match(pattern, toolName)
	if matched {
		return true
	}

	// Pipe-separated patterns
	if strings.Contains(pattern, "|") {
		for _, p := range strings.Split(pattern, "|") {
			p = strings.TrimSpace(p)
			if m, _ := filepath.Match(p, toolName); m {
				return true
			}
		}
	}

	return false
}

// ExtractFilePath attempts to extract a file path from common tool argument patterns.
func ExtractFilePath(toolName string, rawInput string) string {
	for _, key := range []string{"file_path", "path", "filename", "file"} {
		idx := strings.Index(rawInput, `"`+key+`"`)
		if idx < 0 {
			continue
		}
		rest := rawInput[idx:]
		colonIdx := strings.Index(rest, ":")
		if colonIdx < 0 {
			continue
		}
		val := strings.TrimSpace(rest[colonIdx+1:])
		val = strings.TrimPrefix(val, `"`)
		val = strings.TrimSuffix(val, `"`)
		if i := strings.Index(val, `"`); i > 0 {
			val = val[:i]
		}
		if val != "" {
			return val
		}
	}
	return ""
}
