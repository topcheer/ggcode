package hooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	case EventOnCompaction:
		hooks = cfg.OnCompaction
	default:
		return HookResult{Allowed: true}
	}

	// Async fire-and-forget for stop events.
	if env.Event == EventOnAgentStop || env.Event == EventOnStreamStop || env.Event == EventOnCompaction {
		payload := BuildPayload(env)
		for _, h := range hooks {
			if !matchAny(h.MatchMode, h.Match, env.ToolName, env.RawInput) {
				continue
			}
			h := h // capture for goroutine
			// #547: detach from the caller's cancellable context. These stop
			// hooks run AFTER the session/stream is winding down; inheriting a
			// ctx that gets cancelled at teardown killed the hook process
			// before it could do its job. executeHook falls back to
			// context.Background() when Ctx is nil, so the per-hook timeout
			// still applies and cancellation no longer leaks in.
			asyncEnv := env
			asyncEnv.Ctx = nil
			safego.Go("hooks.async."+env.Event, func() {
				_ = executeHook(h, asyncEnv, payload)
			})
		}
		return HookResult{Allowed: true}
	}

	// Sync execution for on_user_message, pre_tool_use, post_tool_use.
	return runSync(hooks, env)
}

// runSync runs hooks sequentially. For blocking events, the first block wins.
// For post_tool_use, collects inject_output from all matching hooks.
// Hook execution errors (command failure, HTTP >= 400) are collected into the
// returned HookResult.Err (joined) instead of being silently dropped (#547).
func runSync(hooksList []Hook, env HookEnv) HookResult {
	payload := BuildPayload(env)
	var injectedOutput strings.Builder
	var errs []error

	for _, h := range hooksList {
		if !matchAny(h.MatchMode, h.Match, env.ToolName, env.RawInput) {
			continue
		}
		result := executeHook(h, env, payload)
		if !result.Allowed {
			return result
		}
		if result.Err != nil {
			// Non-blocking failure: record it but keep running remaining hooks.
			errs = append(errs, fmt.Errorf("%s (match=%q): %w", env.Event, h.Match, result.Err))
		}
		if env.Event == EventPostToolUse && h.InjectOutput && result.Output != "" {
			injectedOutput.WriteString(result.Output)
			if !strings.HasSuffix(result.Output, "\n") {
				injectedOutput.WriteString("\n")
			}
		}
	}

	res := HookResult{Allowed: true, Output: injectedOutput.String()}
	if len(errs) > 0 {
		res.Err = errors.Join(errs...)
	}
	return res
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

	// Apply configured timeout (default 10s for sync hooks).
	timeout := 10 * time.Second
	if h.Timeout != "" {
		if d, err := time.ParseDuration(h.Timeout); err == nil && d > 0 {
			timeout = d
		}
	}
	baseCtx := env.Ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	ctx, cancel := context.WithTimeout(baseCtx, timeout)
	defer cancel()

	// Template expansion — only known vars, preserve unknown for shell.
	// #566(D): every expansion value is shell-quoted so file paths with
	// spaces/quotes stay a single word and RAW_INPUT content can never break
	// out of its argument position (command injection via "; rm ...").
	// Hook authors could not defend in the template itself — the shell
	// re-parses quotes inside the expanded value either way.
	expanded := os.Expand(h.Command, func(key string) string {
		switch key {
		case "TOOL_NAME":
			return shellQuote(env.ToolName)
		case "FILE_PATH":
			return shellQuote(env.FilePath)
		case "WORKING_DIR":
			return shellQuote(env.WorkingDir)
		case "RAW_INPUT":
			return shellQuote(env.RawInput)
		case "TOOL_SUCCESS":
			return strconv.FormatBool(env.ToolSuccess)
		case "TOOL_ERROR":
			return shellQuote(env.ToolError)
		case "TOOL_RESULT":
			return shellQuote(env.ToolResult)
		case "TOOL_DURATION":
			return shellQuote(env.ToolDuration)
		case "EVENT":
			return shellQuote(env.Event)
		case "PAYLOAD":
			return shellQuote(payloadJSON)
		default:
			return "${" + key + "}"
		}
	})

	c, _, err := util.NewShellCommandContext(ctx, expanded)
	if err != nil {
		return HookResult{Allowed: true, Err: fmt.Errorf("resolve hook shell: %w", err)}
	}
	c.Dir = env.WorkingDir
	// #413: drop inherited GGCODE_* keys from os.Environ() before injecting
	// ours. Otherwise the envp carries duplicate keys and the inherited value
	// (e.g. set by a chained hook that spawned this ggcode) wins on getenv,
	// shadowing the fresh payload.
	c.Env = buildHookEnv(env, payloadJSON)
	// #413: put the hook in its own process group and kill the whole group
	// on timeout — the default context kill only reaps the shell itself,
	// leaving `cmd &` background children adopted by init.
	setProcessGroupKill(c)
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
		if d, err := time.ParseDuration(h.Timeout); err == nil && d > 0 {
			timeout = d
		}
	}

	method := h.Method
	if method == "" {
		method = "POST"
	}

	baseCtx := env.Ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	ctx, cancel := context.WithTimeout(baseCtx, timeout)
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

// RunCompactionHooks runs on_compaction hooks asynchronously (fire-and-forget).
// env should include TokenBefore and TokenAfter for the hook to see how much
// context was reclaimed.
func RunCompactionHooks(cfg HookConfig, env HookEnv) {
	env.Event = EventOnCompaction
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
	// #413: evaluate each pipe-separated alternative independently. The old
	// code entered the function-call branch on the FIRST "(" anywhere in the
	// pattern, so a mixed pattern like `edit_file(*)|bash` sliced
	// patArgs="*)|bas" from the whole string — Contains was always false and
	// the hook silently never fired. Splitting first makes each alternative
	// parse with exactly one syntax.
	if strings.Contains(pattern, "|") {
		for _, alt := range strings.Split(pattern, "|") {
			alt = strings.TrimSpace(alt)
			if alt == "" {
				continue
			}
			if matchToolSingle(alt, toolName, rawInput) {
				return true
			}
		}
		return false
	}
	return matchToolSingle(pattern, toolName, rawInput)
}

// matchToolSingle matches one pattern alternative (no pipes) against a tool
// call. Two syntaxes: function-call `tool(args)` and simple glob on the tool
// name.
func matchToolSingle(pattern, toolName, rawInput string) bool {
	// Function call pattern: tool_name(args...)
	if parenIdx := strings.Index(pattern, "("); parenIdx > 0 {
		// Guard against malformed patterns (#429): "edit_file(" (no content
		// after paren) AND "edit_file(x" (missing closing paren) must be
		// rejected. The old code stripped the last char unconditionally, so
		// "edit_file(x" became patArgs="" and matched EVERY invocation — a
		// config typo silently widened the hook to a wildcard.
		if parenIdx+1 > len(pattern)-1 || !strings.HasSuffix(pattern, ")") {
			return false
		}
		patTool := pattern[:parenIdx]
		patArgs := pattern[parenIdx+1 : len(pattern)-1]

		if patTool != toolName {
			return false
		}
		// patArgs=="" here means the well-formed bare form "tool()" — an
		// explicit match-all (now guaranteed well-formed by HasSuffix).
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
	return matched
}

// maxHookEnvValue caps each GGCODE_* env value injected into hook
// processes (#566-C). 128KB keeps execve safely under typical E2BIG
// (256KB-1MB on Linux/macOS) even with several large values present.
const maxHookEnvValue = 128 * 1024

// truncateHookEnv truncates v to maxHookEnvValue bytes, snapping to a rune
// boundary so multi-byte UTF-8 is never split. Returns (value, truncated).
func truncateHookEnv(v string) (string, bool) {
	if len(v) <= maxHookEnvValue {
		return v, false
	}
	cut := util.SnapToRuneStart(v, maxHookEnvValue)
	return v[:cut], true
}

// shellQuote wraps s in single quotes, escaping embedded single quotes as
// '\” — the standard POSIX-safe quoting. The result is always a single word
// to the shell regardless of spaces, quotes, $, backticks, or ';'.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// buildHookEnv assembles the hook process environment: inherited environ
// minus stale GGCODE_* keys (#413), plus fresh values. Oversized values are
// truncated to maxHookEnvValue (#566-C): a 1MB RawInput previously made
// fork/exec fail with E2BIG — the hook never started, yet pre_tool_use hooks
// reported Allowed=true, silently bypassing audit/blocking hooks for large
// tool inputs. The full payload still reaches the hook on stdin.
func buildHookEnv(env HookEnv, payloadJSON string) []string {
	truncPayload, payloadTrunc := truncateHookEnv(payloadJSON)
	truncRaw, rawTrunc := truncateHookEnv(env.RawInput)
	if payloadTrunc || rawTrunc {
		debug.Log("hooks", "%s: env value truncated to %d bytes (payload=%v raw=%v)",
			env.Event, maxHookEnvValue, payloadTrunc, rawTrunc)
	}
	extra := []string{
		"GGCODE_HOOK_PAYLOAD=" + truncPayload,
		"GGCODE_HOOK_EVENT=" + env.Event,
		"GGCODE_RAW_INPUT=" + truncRaw,
		"GGCODE_TOOL_NAME=" + env.ToolName,
		"GGCODE_TOOL_SUCCESS=" + strconv.FormatBool(env.ToolSuccess),
		"GGCODE_TOOL_ERROR=" + env.ToolError,
		"GGCODE_TOOL_RESULT=" + env.ToolResult,
		"GGCODE_TOOL_DURATION=" + env.ToolDuration,
	}
	if rawTrunc {
		extra = append(extra, "GGCODE_RAW_INPUT_TRUNCATED=1")
	}
	return append(filterEnviron(os.Environ(),
		"GGCODE_HOOK_PAYLOAD", "GGCODE_HOOK_EVENT", "GGCODE_RAW_INPUT",
		"GGCODE_RAW_INPUT_TRUNCATED",
		"GGCODE_TOOL_NAME", "GGCODE_TOOL_SUCCESS", "GGCODE_TOOL_ERROR",
		"GGCODE_TOOL_RESULT", "GGCODE_TOOL_DURATION",
	), extra...)
}

// filterEnviron removes entries whose key matches one of keys, so injected
// values never collide with inherited ones (#413).
func filterEnviron(environ []string, keys ...string) []string {
	drop := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		drop[k] = struct{}{}
	}
	out := environ[:0:0]
	for _, kv := range environ {
		i := strings.IndexByte(kv, '=')
		if i > 0 {
			if _, bad := drop[kv[:i]]; bad {
				continue
			}
		}
		out = append(out, kv)
	}
	return out
}

// pathParamFields lists tool argument fields that hold a filesystem path.
// Order matters: most specific first.
var pathParamFields = []string{"file_path", "path", "filename", "file"}

// contentValueFields lists argument fields whose string value is arbitrary
// user/agent content rather than a path. ExtractFilePath ignores these keys
// entirely so example paths embedded in content (e.g. a JSON config sample
// inside write_file's content) are never mistaken for the target file (#547).
var contentValueFields = []string{"content", "body", "text", "new_text", "old_text"}

// ExtractFilePath attempts to extract a file path from common tool argument patterns.
// It parses rawInput as JSON and inspects only structured top-level fields;
// values nested inside content-like fields are never scanned (#547).
func ExtractFilePath(toolName string, rawInput string) string {
	trimmed := strings.TrimSpace(rawInput)
	if trimmed == "" {
		return ""
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(trimmed), &args); err != nil {
		return "" // not structured JSON — no reliable path to extract
	}
	for _, contentKey := range contentValueFields {
		delete(args, contentKey)
	}
	for _, key := range pathParamFields {
		val, ok := args[key]
		if !ok {
			continue
		}
		s, ok := val.(string)
		if !ok || s == "" {
			continue // non-string JSON value (null/number/bool) — skip
		}
		return s
	}
	return ""
}
