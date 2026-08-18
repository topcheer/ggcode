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

// runSync runs hooks sequentially. For blocking events (pre_tool_use,
// on_user_message), the first block wins. post_tool_use never blocks —
// it only collects inject_output from all matching hooks (#679).
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
		if !result.Allowed && isBlockingEvent(env.Event) {
			// Only blocking events short-circuit on a block. post_tool_use
			// hooks cannot un-run the tool, so honoring a block there dropped
			// the inject_output already collected and skipped remaining
			// hooks (#679).
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
		// #684: a policy verdict (exit 2 / HTTP 403) on a non-blocking event
		// cannot be honored as a block, but its reason must still reach the
		// model — every consumer of post hook results reads only Output. Unlike
		// inject_output this is NOT gated on InjectOutput: the hook author
		// explicitly flagged something, not answered a request for output.
		if env.Event == EventPostToolUse && result.PolicyNotice != "" {
			injectedOutput.WriteString(result.PolicyNotice)
			if !strings.HasSuffix(result.PolicyNotice, "\n") {
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

// isBlockingEvent reports whether block semantics (command exit 2 /
// HTTP 403 → Allowed=false) apply to this event. Defined for
// pre_tool_use and on_user_message only (#679): post_tool_use hooks run
// after the tool has already executed — an exit 2 there cannot un-run
// it, so honoring it just dropped collected inject_output and skipped
// the remaining hooks.
func isBlockingEvent(event string) bool {
	return event == EventPreToolUse || event == EventOnUserMessage
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
// Exit code 2 = block (blocking events only — pre_tool_use,
// on_user_message; #679). Stdout captured for inject_output.
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
	expanded := expandHookTemplate(h.Command, env, payloadJSON)

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
		// Exit 2 = block, but blocking events only (#679). A post_tool_use
		// hook exiting 2 cannot un-run the tool; honoring it turned the
		// exit into a fake block that dropped collected inject_output and
		// skipped the remaining hooks. Non-blocking events fall through to
		// the generic error path (collected into errs by runSync).
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
		// #684: even when exit 2 is not honored as a block (non-blocking
		// events), the hook author's reason must survive — stderr is not part
		// of exec.ExitError and nothing downstream read Err for the reason.
		// Surface it as a PolicyNotice so runSync folds it into Output.
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
				// Other failures: fold a one-line stderr into the error so the
				// reason is diagnosable in the joined error string.
				return HookResult{Allowed: true, Output: stdout.String(),
					Err: fmt.Errorf("hook command failed: %w%s", err, stderrSuffix(stderr.String()))}
			}
		}
		return HookResult{Allowed: true, Output: stdout.String(), Err: fmt.Errorf("hook command failed: %w", err)}
	}

	return HookResult{Allowed: true, Output: stdout.String()}
}

// expandHookTemplate expands $VAR references in a hook command template.
// Only known vars, preserve unknown for shell.
// #566(D): every expansion value is shell-quoted so file paths with
// spaces/quotes stay a single word and RAW_INPUT content can never break
// out of its argument position (command injection via "; rm ...").
// Hook authors could not defend in the template itself — the shell
// re-parses quotes inside the expanded value either way.
// #679: RAW_INPUT and PAYLOAD are additionally capped at maxHookEnvValue
// before quoting — the expanded value becomes part of the shell command
// line (argv); past 64KB it crosses Linux MAX_ARG_STRLEN, execve fails
// E2BIG, the hook never starts, and the pre_tool_use audit silently
// passes (same rationale as the envp cap in buildHookEnv). The full input
// still reaches the hook via stdin.
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
			// #684: a raw byte-cap can cut the JSON mid-escape/mid-literal and
			// hand hooks INVALID JSON — an audit hook's jq/python then fails,
			// takes the generic error path, and the check silently passes
			// (fail-open). Snap the cut to a JSON token boundary instead; when
			// no clean cut exists, drop the payload to a short explicit
			// truncation marker so hooks fail visibly on a well-formed doc.
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

// executeHTTPHook sends an HTTP POST with the standardized payload.
// HTTP 403 = block (blocking events only — pre_tool_use, on_user_message;
// #679). Response body captured for inject_output.
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

	// HTTP 403 = block, blocking events only (#679) — same event rule as
	// exit 2. Non-blocking events fall through to the >=400 error path.
	if resp.StatusCode == http.StatusForbidden {
		blockMsg := strings.TrimSpace(string(body))
		if isBlockingEvent(env.Event) {
			debug.Log("hooks", "%s BLOCKED: HTTP %d body=%s", env.Event, resp.StatusCode, blockMsg)
			return HookResult{
				Allowed: false,
				Output:  fmt.Sprintf("Blocked by %s hook: %s", env.Event, blockMsg),
			}
		}
		// #684: non-blocking 403 still carries the author's reason — keep it
		// visible as a PolicyNotice instead of vanishing into "hook HTTP 403".
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
		// #679: a trailing `*` is a prefix anchor on the extracted path, not
		// a no-op over the whole argument JSON. The old code ran
		// Contains(rawInput, prefix) — byte-identical to the no-star branch —
		// against the ENTIRE JSON including content fields (new_text/
		// old_text), so `edit_file(internal/*)` fired when the edit content
		// merely mentioned "internal/" and an exit-2 audit hook mis-blocked
		// the call. Now: true path prefix first; when a path exists but is
		// not prefixed, the secondary Contains runs over content-stripped
		// JSON only (path-like field names still match, #429); the legacy
		// whole-JSON Contains survives only when no structured path can be
		// extracted (run_command argument matching, #413).
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
		return strings.Contains(rawInput, patArgs)
	}

	// Simple glob match on tool name
	matched, _ := filepath.Match(pattern, toolName)
	return matched
}

// maxHookEnvValue caps each GGCODE_* env value injected into hook
// processes (#566-C). 64KB keeps execve safely under E2BIG on every
// platform: Linux MAX_ARG_STRLEN caps the ENTIRE "NAME=value" string at
// 131072 bytes (32 pages) — a 128KB value plus the variable-name prefix
// still exceeded it on CI Linux runners (darwin has no per-string cap,
// which is why the 128KB version passed locally). 64KB budgets for any
// GGCODE_* variable name while staying large for realistic payloads.
const maxHookEnvValue = 64 * 1024

// truncateHookEnv truncates v to maxHookEnvValue bytes, snapping to a rune
// boundary so multi-byte UTF-8 is never split. Returns (value, truncated).
func truncateHookEnv(v string) (string, bool) {
	if len(v) <= maxHookEnvValue {
		return v, false
	}
	cut := util.SnapToRuneStart(v, maxHookEnvValue)
	return v[:cut], true
}

// hookJSONTruncationMarker is the document injected when a JSON payload does
// not fit the argv budget and cannot be cut cleanly. It is deliberately small
// and itself valid JSON-embeddable text: hooks parsing it see a well-formed
// (if useless) string instead of a parse crash, and the marker names the real
// recovery path (stdin carries the full payload).
const hookJSONTruncationMarker = `{"truncated": true, "reason": "payload exceeds argv limit; read full payload from stdin"}`

// truncateHookEnvJSON caps a JSON document for the argv channel while keeping
// it parseable (#684). Byte-cap truncation that lands inside a string
// literal or escape sequence produces invalid JSON — audit hooks feeding it
// to jq/python fail, fall into the generic error path, and silently pass
// (fail-open). Strategy, in order:
//  1. If the doc fits, return it unchanged.
//  2. Try to cut at a structural boundary (outside any string literal) and
//     repair the prefix into a complete document. Only attempted when the
//     prefix still closes the root object; otherwise parsing would break.
//  3. Fall back to an explicit truncation marker — visible failure with a
//     pointer to stdin, never silent corruption.
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

// truncateJSONDocument cuts v to at most limit bytes at a position that is
// structurally safe for JSON (outside any string literal) and repairs the
// prefix into a complete document by closing open containers. Returns
// (repaired, ok); ok is false when no safe cut exists (e.g. the entire budget
// is consumed by one giant string literal).
func truncateJSONDocument(v string, limit int) (string, bool) {
	// Find the largest cut position <= limit that is outside a string
	// literal and at a token boundary. Scan with a minimal JSON tokenizer:
	// depth tracking plus in-string/escape state.
	const (
		inCode = iota
		inString
		inEscape
	)
	state := inCode
	depth := 0
	bestCut := -1
	end := len(v)
	if end > limit {
		end = limit
	}
	// Start at 1 so bestCut never lands inside the leading '{'.
	for i := 0; i < end; i++ {
		c := v[i]
		switch state {
		case inCode:
			switch c {
			case '"':
				state = inString
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				// After a closed container we are at a safe cut point.
				bestCut = i + 1
			case ',':
				// A comma ends a member; cutting right after it leaves a
				// dangling separator that must be repaired away — handled
				// below by trimming trailing commas.
				bestCut = i + 1
			}
		case inString:
			if c == '\\' {
				state = inEscape
			} else if c == '"' {
				state = inCode
				// Position right after a complete string is safe only when
				// it is a value/member boundary; the next byte decides, so
				// record it as a candidate — trailing-comma cleanup repairs.
				bestCut = i + 1
			}
		case inEscape:
			state = inString
		}
	}
	if bestCut <= 0 {
		return "", false
	}
	prefix := strings.TrimRight(v[:bestCut], " \t\r\n")
	// Repair: strip a dangling separator left by a cut after ','.
	prefix = strings.TrimSuffix(prefix, ",")
	prefix = strings.TrimRight(prefix, " \t\r\n")
	if prefix == "" {
		return "", false
	}
	// Validate that the prefix is itself complete JSON: the state machine
	// above only tracks strings, so re-parse cheaply with json.Valid on the
	// candidate before accepting it.
	if !json.Valid([]byte(prefix)) {
		return "", false
	}
	return prefix, true
}

// formatPolicyNotice builds the non-blocking policy verdict label shared by
// the exit-2 and HTTP-403 paths (#684).
func formatPolicyNotice(event, reason string) string {
	return fmt.Sprintf("[%s policy: %s]", event, reason)
}

// stderrSuffix formats a one-line stderr tail for error wrapping. #684:
// exec.ExitError does not carry stderr, so the hook author's failure reason
// vanished into an unread buffer. Empty stderr yields "" so %s-suffixed
// error strings stay clean.
func stderrSuffix(stderr string) string {
	if strings.TrimSpace(stderr) == "" {
		return ""
	}
	// Collapse to the first non-empty line, capped, to keep the joined
	// per-hook error readable when a hook spews a stack trace.
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

// stripContentValues re-serializes rawInput as JSON with content-like values
// (content/body/text/new_text/old_text) removed, so substring matching can
// never fire on user/agent content — only on structural field names and path
// values (#679). Returns "" when rawInput is not structured JSON.
func stripContentValues(rawInput string) string {
	trimmed := strings.TrimSpace(rawInput)
	if trimmed == "" {
		return ""
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(trimmed), &args); err != nil {
		return ""
	}
	for _, k := range contentValueFields {
		delete(args, k)
	}
	b, err := json.Marshal(args)
	if err != nil {
		return ""
	}
	return string(b)
}

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
