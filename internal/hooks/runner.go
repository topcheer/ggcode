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
	case EventPreCompact:
		hooks = cfg.PreCompact
	case EventOnSessionStart:
		hooks = cfg.OnSessionStart
	case EventOnSessionEnd:
		hooks = cfg.OnSessionEnd
	default:
		return HookResult{Allowed: true}
	}

	if env.Event == EventOnAgentStop || env.Event == EventOnStreamStop || env.Event == EventOnCompaction {
		payload := BuildPayload(env)
		for _, h := range hooks {
			if !matchAny(h.MatchMode, h.Match, env.ToolName, env.RawInput) {
				continue
			}
			h := h

			asyncEnv := env
			asyncEnv.Ctx = nil
			safego.Go("hooks.async."+env.Event, func() {
				_ = executeHook(h, asyncEnv, payload)
			})
		}
		return HookResult{Allowed: true}
	}

	return runSync(hooks, env)
}

func runSync(hooksList []Hook, env HookEnv) HookResult {
	payload := BuildPayload(env)
	var injectedOutput strings.Builder
	var errs []error

	for _, h := range hooksList {
		if !matchAny(h.MatchMode, h.Match, env.ToolName, env.RawInput) {
			continue
		}
		result := executeHook(h, env, payload)
		if !result.Allowed && isBlockingEvent(env.Event) {

			return result
		}
		if result.Err != nil {

			errs = append(errs, fmt.Errorf("%s (match=%q): %w", env.Event, h.Match, result.Err))
		}
		if env.Event == EventPostToolUse && h.InjectOutput && result.Output != "" {
			injectedOutput.WriteString(result.Output)
			if !strings.HasSuffix(result.Output, "\n") {
				injectedOutput.WriteString("\n")
			}
		}

		if env.Event == EventPostToolUse && result.PolicyNotice != "" {
			injectedOutput.WriteString(result.PolicyNotice)
			if !strings.HasSuffix(result.PolicyNotice, "\n") {
				injectedOutput.WriteString("\n")
			}
		}

		if env.Event == EventOnSessionStart && result.Output != "" {
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

func isBlockingEvent(event string) bool {
	return event == EventPreToolUse || event == EventOnUserMessage || event == EventOnSessionStart
}

func executeHook(h Hook, env HookEnv, payload HookPayload) HookResult {
	switch h.HasType() {
	case HookTypeHTTP:
		return executeHTTPHook(h, env, payload)
	default:
		return executeCommandHook(h, env, payload)
	}
}

func executeCommandHook(h Hook, env HookEnv, payload HookPayload) HookResult {
	payloadJSON := string(payload.JSON())

	debug.Log("hooks", "%s: type=command tool=%s match=%s", env.Event, env.ToolName, h.Match)

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

	expanded := expandHookTemplate(h.Command, env, payloadJSON)

	c, _, err := util.NewShellCommandContext(ctx, expanded)
	if err != nil {
		return HookResult{Allowed: true, Err: fmt.Errorf("resolve hook shell: %w", err)}
	}
	c.Dir = env.WorkingDir

	c.Env = buildHookEnv(env, payloadJSON)

	setProcessGroupKill(c)

	c.Stdin = strings.NewReader(payloadJSON)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr

	err = c.Run()
	if err != nil {

		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 2 && isBlockingEvent(env.Event) {
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

		if exitErr, ok := err.(*exec.ExitError); ok {
			if reason := strings.TrimSpace(stderr.String()); reason != "" {
				if exitErr.ExitCode() == 2 && !isBlockingEvent(env.Event) {
					debug.Log("hooks", "%s POLICY (non-blocking exit 2): tool=%s reason=%s", env.Event, env.ToolName, reason)
					return HookResult{
						Allowed:      true,
						Output:       stdout.String(),
						PolicyNotice: fmt.Sprintf("[%s policy: %s]", env.Event, reason),
						Err:          err,
					}
				}

				return HookResult{Allowed: true, Output: stdout.String(),
					Err: fmt.Errorf("hook command failed: %w%s", err, stderrSuffix(stderr.String()))}
			}
		}
		return HookResult{Allowed: true, Output: stdout.String(), Err: fmt.Errorf("hook command failed: %w", err)}
	}

	return HookResult{Allowed: true, Output: stdout.String()}
}

func expandHookTemplate(command string, env HookEnv, payloadJSON string) string {
	return os.Expand(command, func(key string) string {
		switch key {
		case "TOOL_NAME":
			return shellQuote(env.ToolName)
		case "FILE_PATH":
			return shellQuote(env.FilePath)
		case "WORKING_DIR":
			return shellQuote(env.WorkingDir)
		case "RAW_INPUT":

			return shellQuote(truncateHookEnvJSON(env.RawInput))
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
			return shellQuote(truncateHookEnvJSON(payloadJSON))
		default:
			return "${" + key + "}"
		}
	})
}

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

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))

	if resp.StatusCode == http.StatusForbidden {
		blockMsg := strings.TrimSpace(string(body))
		if isBlockingEvent(env.Event) {
			debug.Log("hooks", "%s BLOCKED: HTTP %d body=%s", env.Event, resp.StatusCode, blockMsg)
			return HookResult{
				Allowed: false,
				Output:  fmt.Sprintf("Blocked by %s hook: %s", env.Event, blockMsg),
			}
		}

		debug.Log("hooks", "%s POLICY (non-blocking 403): body=%s", env.Event, blockMsg)
		return HookResult{
			Allowed:      true,
			Output:       string(body),
			PolicyNotice: formatPolicyNotice(env.Event, blockMsg),
		}
	}

	if resp.StatusCode >= 400 {
		debug.Log("hooks", "%s HTTP %d (non-blocking)", env.Event, resp.StatusCode)
		return HookResult{Allowed: true, Err: fmt.Errorf("hook HTTP %d", resp.StatusCode)}
	}

	return HookResult{Allowed: true, Output: string(body)}
}

func RunPreHooks(hooks []Hook, env HookEnv) HookResult {
	env.Event = EventPreToolUse
	return runSync(hooks, env)
}

func RunPostHooks(hooks []Hook, env HookEnv) HookResult {
	env.Event = EventPostToolUse
	return runSync(hooks, env)
}

func RunUserMessageHooks(hooks []Hook, env HookEnv) HookResult {
	env.Event = EventOnUserMessage
	return runSync(hooks, env)
}

func RunAgentStopHooks(cfg HookConfig, env HookEnv) {
	env.Event = EventOnAgentStop
	Dispatch(cfg, env)
}

func RunStreamStopHooks(cfg HookConfig, env HookEnv) {
	env.Event = EventOnStreamStop
	Dispatch(cfg, env)
}

func RunCompactionHooks(cfg HookConfig, env HookEnv) {
	env.Event = EventOnCompaction
	Dispatch(cfg, env)
}

func RunPreCompactHooks(cfg HookConfig, env HookEnv) HookResult {
	env.Event = EventPreCompact
	return Dispatch(cfg, env)
}

func RunSessionStartHooks(cfg HookConfig, env HookEnv) HookResult {
	env.Event = EventOnSessionStart
	return Dispatch(cfg, env)
}

func RunSessionEndHooks(cfg HookConfig, env HookEnv) HookResult {
	env.Event = EventOnSessionEnd
	return Dispatch(cfg, env)
}

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

func matchTool(pattern, toolName, rawInput string) bool {

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

func matchToolSingle(pattern, toolName, rawInput string) bool {

	if parenIdx := strings.Index(pattern, "("); parenIdx > 0 {

		if parenIdx+1 > len(pattern)-1 || !strings.HasSuffix(pattern, ")") {
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
			if p := ExtractFilePath(toolName, rawInput); p != "" {
				if strings.HasPrefix(p, prefix) {
					return true
				}
				return strings.Contains(stripContentValues(rawInput), prefix)
			}
			return strings.Contains(rawInput, prefix)
		}

		if p := ExtractFilePath(toolName, rawInput); p != "" {
			return strings.Contains(p, patArgs)
		}
		return strings.Contains(stripContentValues(rawInput), patArgs)
	}

	matched, _ := filepath.Match(pattern, toolName)
	return matched
}

const maxHookEnvValue = 64 * 1024

func truncateHookEnv(v string) (string, bool) {
	if len(v) <= maxHookEnvValue {
		return v, false
	}
	cut := util.SnapToRuneStart(v, maxHookEnvValue)
	return v[:cut], true
}

const hookJSONTruncationMarker = `{"truncated": true, "reason": "payload exceeds argv limit; read full payload from stdin"}`

func truncateHookEnvJSON(v string) string {
	if len(v) <= maxHookEnvValue {
		return v
	}
	if repaired, ok := truncateJSONDocument(v, maxHookEnvValue); ok {
		return repaired
	}
	debug.Log("hooks", "payload %d bytes exceeds %d argv budget; replacing with truncation marker (full payload on stdin)",
		len(v), maxHookEnvValue)
	return hookJSONTruncationMarker
}

type jsonCutCandidate struct {
	pos     int
	closers string
}

func jsonClosersFor(stack []byte) string {
	var b strings.Builder
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == '{' {
			b.WriteByte('}')
		} else {
			b.WriteByte(']')
		}
	}
	return b.String()
}

func truncateJSONDocument(v string, limit int) (string, bool) {

	const (
		inCode = iota
		inString
		inEscape
	)
	state := inCode
	var stack []byte
	var cuts []jsonCutCandidate
	end := len(v)
	if end > limit {
		end = limit
	}
	for i := 0; i < end; i++ {
		c := v[i]
		switch state {
		case inCode:
			switch c {
			case '"':
				state = inString
			case '{', '[':
				stack = append(stack, c)
			case '}', ']':
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
				// Right after a closed container is a safe cut point.
				cuts = append(cuts, jsonCutCandidate{pos: i + 1, closers: jsonClosersFor(stack)})
			case ',':
				// A comma ends a member; cutting right after it leaves a
				// dangling separator — trimmed below.
				if len(stack) > 0 {
					cuts = append(cuts, jsonCutCandidate{pos: i + 1, closers: jsonClosersFor(stack)})
				}
			}
		case inString:
			if c == '\\' {
				state = inEscape
			} else if c == '"' {
				state = inCode

				cuts = append(cuts, jsonCutCandidate{pos: i + 1, closers: jsonClosersFor(stack)})
			}
		case inEscape:
			state = inString
		}
	}

	for i := len(cuts) - 1; i >= 0; i-- {
		cut := cuts[i]
		prefix := strings.TrimRight(v[:cut.pos], " \t\r\n")

		prefix = strings.TrimSuffix(prefix, ",")
		prefix = strings.TrimRight(prefix, " \t\r\n")
		if prefix == "" {
			continue
		}
		repaired := prefix + cut.closers
		if len(repaired) > limit {
			continue
		}
		if !json.Valid([]byte(repaired)) {
			continue
		}
		return repaired, true
	}
	return "", false
}

func formatPolicyNotice(event, reason string) string {
	return fmt.Sprintf("[%s policy: %s]", event, reason)
}

func stderrSuffix(stderr string) string {
	if strings.TrimSpace(stderr) == "" {
		return ""
	}

	line := strings.TrimSpace(stderr)
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	const maxStderrTail = 200
	line = strings.TrimSpace(line)
	if len(line) > maxStderrTail {
		cut := util.SnapToRuneStart(line, maxStderrTail)
		line = line[:cut] + "..."
	}
	return " (stderr: " + line + ")"
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

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
		"GGCODE_SESSION_SOURCE=" + env.SessionSource,
		"GGCODE_SESSION_END_REASON=" + env.SessionEndReason,
	}
	if rawTrunc {
		extra = append(extra, "GGCODE_RAW_INPUT_TRUNCATED=1")
	}
	return append(filterEnviron(os.Environ(),
		"GGCODE_HOOK_PAYLOAD", "GGCODE_HOOK_EVENT", "GGCODE_RAW_INPUT",
		"GGCODE_RAW_INPUT_TRUNCATED",
		"GGCODE_TOOL_NAME", "GGCODE_TOOL_SUCCESS", "GGCODE_TOOL_ERROR",
		"GGCODE_TOOL_RESULT", "GGCODE_TOOL_DURATION",
		"GGCODE_SESSION_SOURCE", "GGCODE_SESSION_END_REASON",
	), extra...)
}

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

var pathParamFields = []string{
	"file_path", "path", "filename", "file",
	"directory", "source", "destination", "notebook_path",
}

var contentValueFields = []string{"content", "body", "text", "new_text", "old_text"}

func stripContentValues(rawInput string) string {
	trimmed := strings.TrimSpace(rawInput)
	if trimmed == "" {
		return ""
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(trimmed), &args); err != nil {
		return ""
	}
	stripNested(args)
	b, err := json.Marshal(args)
	if err != nil {
		return ""
	}
	return string(b)
}

func stripNested(v any) {
	switch t := v.(type) {
	case map[string]any:
		for _, k := range contentValueFields {
			delete(t, k)
		}
		for _, child := range t {
			stripNested(child)
		}
	case []any:
		for _, child := range t {
			stripNested(child)
		}
	}
}

func ExtractFilePath(toolName string, rawInput string) string {
	trimmed := strings.TrimSpace(rawInput)
	if trimmed == "" {
		return ""
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(trimmed), &args); err != nil {
		return ""
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
			continue
		}
		return s
	}
	return ""
}
