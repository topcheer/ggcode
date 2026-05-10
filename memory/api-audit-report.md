# API Design & Error Handling Audit Report

**Auditor:** api-auditor
**Date:** 2025-07-27
**Scope:** `internal/agent/`, `internal/provider/`, `internal/tool/`, `internal/mcp/`, `internal/a2a/`, `internal/config/`, `internal/webui/`, `internal/subagent/`, `internal/swarm/`, `internal/plugin/`

---

## 1. Interface Consistency

### Finding 1.1 — CommandTool.Execute is a Placeholder (High)
- **File:** `internal/plugin/plugin.go:102-108`
- **Severity:** High
- **Category:** Interface Consistency / Completeness
- **Description:** `CommandTool.Execute()` returns a hardcoded placeholder string `"Command tool %q executed (placeholder)"` instead of actually running the external command. Any plugin using `CommandTool` will silently return a fake result to the LLM, causing it to believe it performed work it never did.
- **Suggested improvement:** Implement actual command execution using `os/exec`, or return a clear error `tool.Result{IsError: true, Content: "command tool not yet implemented"}`.

### Finding 1.2 — Plugin.Init Always Returns nil (Low)
- **File:** `internal/plugin/plugin.go:118`
- **Severity:** Low
- **Category:** Interface Consistency
- **Description:** `commandPlugin.Init()` always returns `nil` without using its `config` parameter. This is technically correct (no-op init), but means plugin configuration from YAML is silently ignored. If a user configures plugin options, they are never read.
- **Suggested improvement:** Document that `commandPlugin` does not use config, or wire config parameters through to the underlying commands.

### Finding 1.3 — webui.ChatBridge vs webui.AgentRunner Duplication (Medium)
- **File:** `internal/webui/server.go:125-142`
- **Severity:** Medium
- **Category:** Interface Consistency / API Surface
- **Description:** `Server` holds both a `chatBridge ChatBridge` (current) and `agent AgentRunner` (legacy) with a comment "legacy, for non-bridge setups". The `AgentRunner` interface is nearly a superset of `ChatBridge`. This dual-path creates confusion about which code path is active and increases maintenance burden.
- **Suggested improvement:** Add a deprecation timeline or remove `AgentRunner` once all callers migrate to `ChatBridge`.

### Finding 1.4 — Swarm AgentFactory Takes `interface{}` for Tools (Medium)
- **File:** `internal/swarm/manager.go:17`
- **Severity:** Medium
- **Category:** Interface Consistency / Type Safety
- **Description:** `AgentFactory` receives `tools interface{}` — an untyped parameter. The same pattern appears in `internal/subagent/runner.go:22`. This erases type safety and forces factory implementations to type-assert at runtime. Any mismatch causes a silent nil tool set or a panic.
- **Suggested improvement:** Consider using `*tool.Registry` instead of `interface{}`, or define a typed `ToolSet` interface.

### Finding 1.5 — Swarm AgentRunner vs SubAgent AgentRunner Duplication (Low)
- **File:** `internal/swarm/manager.go:20-22` vs `internal/subagent/runner.go:25-27`
- **Severity:** Low
- **Category:** Interface Consistency / Duplication
- **Description:** Both packages define identical `AgentRunner` interfaces and `AgentFactory` function types with the same signatures. This is a DRY violation and could drift if one is updated without the other.
- **Suggested improvement:** Extract to a shared package or have one import from the other.

---

## 2. Error Handling Patterns

### Finding 2.1 — Operator Precedence Bug in A2A Client tryBearerToken (High)
- **File:** `internal/a2a/client.go:188`
- **Severity:** High
- **Category:** Error Handling / Logic Bug
- **Description:** The condition reads:
  ```go
  if c.bearerToken != "" && c.tokenExpiry.IsZero() || time.Now().Before(c.tokenExpiry) {
  ```
  Due to Go operator precedence (`&&` binds tighter than `||`), this evaluates as:
  ```
  (c.bearerToken != "" && c.tokenExpiry.IsZero()) || time.Now().Before(c.tokenExpiry)
  ```
  This means if `time.Now().Before(c.tokenExpiry)` is true (even with an empty `bearerToken`), it returns true without a valid token. The right side can also panic or return unexpected results when `tokenExpiry` is zero (zero time is always "before" now). The intended logic is likely:
  ```go
  if c.bearerToken != "" && (c.tokenExpiry.IsZero() || time.Now().Before(c.tokenExpiry)) {
  ```
- **Suggested improvement:** Add parentheses to fix the intended precedence.

### Finding 2.2 — A2A Client rpc() Silently Swallows JSON Marshal Errors (Medium)
- **File:** `internal/a2a/client.go:459, 466`
- **Severity:** Medium
- **Category:** Error Handling / Swallowed Errors
- **Description:** Two `json.Marshal` calls in `rpc()` use `_` to discard errors:
  ```go
  paramsJSON, _ := json.Marshal(params)
  body, _ := json.Marshal(rpcReq)
  ```
  If `params` contains unmarshallable values (channels, functions), `paramsJSON` will be `nil`, producing an invalid request body sent to the server. The same applies to line 496: `resultJSON, _ := json.Marshal(rpcResp.Result)`.
- **Suggested improvement:** Check errors from `json.Marshal` and return them wrapped with context.

### Finding 2.3 — A2A Client decodeSSE Silently Drops Unparseable Events (Medium)
- **File:** `internal/a2a/client.go:521`
- **Severity:** Medium
- **Category:** Error Handling / Swallowed Errors
- **Description:** In `decodeSSE`, unparseable JSON events are silently dropped:
  ```go
  if json.Unmarshal([]byte(data), &resp) == nil {
      ch <- resp
  }
  ```
  No logging or error reporting occurs for malformed SSE data. This can mask server-side protocol violations.
- **Suggested improvement:** Log unparseable events via `debug.Log` and consider sending an error on the channel after multiple consecutive failures.

### Finding 2.4 — MCP Client writeMessage Does Not Wrap Errors (Medium)
- **File:** `internal/mcp/client.go:383-398`
- **Severity:** Medium
- **Category:** Error Handling / Inconsistent Wrapping
- **Description:** `writeMessage()` returns raw `json.Marshal` and `stdin.Write` errors without the `mcp[name]:` context prefix used elsewhere in the same file (e.g., `sendHTTP`, `sendWS`). When this error surfaces, the caller cannot identify which MCP server failed.
- **Suggested improvement:** Wrap errors with `fmt.Errorf("mcp[%s]: write message: %w", c.name, err)`.

### Finding 2.5 — MCP Client Multiple Bare `return nil, err` Without Wrapping (Medium)
- **File:** `internal/mcp/client.go:344, 462, 477, 485, 504` and `internal/mcp/jsonrpc.go:70, 77, 86, 93`
- **Severity:** Medium
- **Category:** Error Handling / Inconsistent Wrapping
- **Description:** Multiple places in the MCP client return raw errors without wrapping:
  - `client.go:344`: `return nil, err` after `writeMessage`
  - `client.go:462`: `return nil, err` after `json.Marshal` in `sendWS`
  - `client.go:477`: `return nil, err` after `ctx.Err()` check in `sendWS`
  - `client.go:485`: `return nil, err` after `ParseMessage` in `sendWS`
  - `jsonrpc.go:70,77,86,93`: bare `return nil, err` after JSON unmarshal failures
  
  These lose the `mcp[name]` context that other methods carefully attach.
- **Suggested improvement:** Wrap all error returns with server name context using `fmt.Errorf("mcp[%s]: <operation>: %w", c.name, err)`.

### Finding 2.6 — A2A Handler Accesses History[0] Without Bounds Check (High)
- **File:** `internal/a2a/handler.go:259, 261, 264, 413`
- **Severity:** High
- **Category:** Boundary Conditions / Nil Dereference Risk
- **Description:** The `execute()` method accesses `t.History[0]` in three places and `updateStatus` accesses `t.History[0]` for logging. While `Handle()` creates tasks with `History: []Message{input}`, `continueTask()` appends to history, and tasks loaded from the task map may have empty history if corrupted. There is no bounds check before accessing `[0]`.
- **Suggested improvement:** Add `if len(t.History) == 0` guard before all `t.History[0]` accesses, returning an error if empty.

### Finding 2.7 — A2A executeDirectTool Returns Unwrapped Tool Error (Low)
- **File:** `internal/a2a/handler.go:322-323`
- **Severity:** Low
- **Category:** Error Handling / Missing Context
- **Description:** `executeDirectTool` returns `err` from `t.Execute()` directly without wrapping, losing context about which tool and skill produced the error.
- **Suggested improvement:** Wrap with `fmt.Errorf("tool %s execution for skill %s: %w", toolName, skill, err)`.

### Finding 2.8 — MCP Adapter RegisterTools Silently Continues on Error (Medium)
- **File:** `internal/mcp/adapter.go:48-51`
- **Severity:** Medium
- **Category:** Error Handling / Swallowed Errors
- **Description:** `RegisterTools` catches the error from `registry.Register` and logs it as a warning but continues. If two MCP servers provide a tool with the same local name, one silently fails to register. The caller never learns about the collision.
- **Suggested improvement:** Return the error to the caller, or at least accumulate and return a multi-error after the loop.

### Finding 2.9 — DingTalk Adapter Multiple Ignored json.Marshal Errors (Medium)
- **File:** `internal/im/dingtalk_adapter.go:469, 529, 621, 664`
- **Severity:** Medium
- **Category:** Error Handling / Swallowed Errors
- **Description:** Four instances of `bodyJSON, _ := json.Marshal(body)` where the error is discarded. If the body contains unmarshallable types, the request will be sent with `nil` body.
- **Suggested improvement:** Check errors and return them from the calling function.

### Finding 2.10 — lsp/operations.go Ignores json.Marshal Error (Low)
- **File:** `internal/lsp/operations.go:222`
- **Severity:** Low
- **Category:** Error Handling / Swallowed Error
- **Description:** `data, _ := json.Marshal(v)` discards the error. Used for debug logging, so impact is low but the `data` variable is used later regardless of success.
- **Suggested improvement:** Guard with `if err != nil { return "" }` or similar.

---

## 3. Boundary Conditions

### Finding 3.1 — truncateStr Truncates by Bytes, Not Runes (Medium)
- **File:** `internal/webui/tui_bridge.go:122-126`
- **Severity:** Medium
- **Category:** Boundary Conditions / Unicode Safety
- **Description:** `truncateStr` uses `len(s) <= max` and `s[:max]` which operates on bytes, not runes. For multi-byte UTF-8 characters (Chinese, Japanese, emoji), this can split a character mid-sequence, producing invalid UTF-8 in debug log output.
- **Suggested improvement:** Use `utf8.RuneCountInString` and rune-based slicing, or `util.Truncate` if it handles this correctly.

### Finding 3.2 — Agent maxIter=0 Means Unlimited (by Design, but Undocumented on Struct) (Low)
- **File:** `internal/agent/agent.go:45, 400`
- **Severity:** Low
- **Category:** Boundary Conditions / Zero-Value Semantics
- **Description:** `maxIter` of 0 means unlimited iterations (line 400: `a.maxIter <= 0 || i < a.maxIter`). This is intentional and tested, but not documented on the `Agent` struct field. The `Config.MaxIterations` field has the same convention but it's easy to miss.
- **Suggested improvement:** Add a comment on the `maxIter` field: `// 0 means unlimited`.

### Finding 3.3 — Swarm Manager ID Generation Uses Shared Counter (Low)
- **File:** `internal/swarm/manager.go:106-108, 185-186`
- **Severity:** Low
- **Category:** Boundary Conditions / Potential ID Collision
- **Description:** Both team IDs (`team-N`) and teammate IDs (`tm-N`) use the same `m.nextTeamID` counter. So a team could be `team-1` and the first teammate `tm-2`, then the next team `team-3`. While IDs are unique, the naming is slightly misleading (teammate IDs don't indicate which team they belong to).
- **Suggested improvement:** Use separate counters for team and teammate IDs.

### Finding 3.4 — Gemini Provider Ignores json.Marshal Error for FunctionCall Args (Low)
- **File:** `internal/provider/gemini.go:200`
- **Severity:** Low
- **Category:** Error Handling / Swallowed Error
- **Description:** `args, _ := json.Marshal(part.FunctionCall.Args)` — the error is discarded. If marshaling fails, `args` will be `nil` and an empty JSON object is sent to the API, causing an opaque server error.
- **Suggested improvement:** Check the error and propagate it or log it.

---

## 4. Naming & Documentation

### Finding 4.1 — webui.AgentRunner Interface Marked Legacy but Still Exported (Low)
- **File:** `internal/webui/server.go:137-142`
- **Severity:** Low
- **Category:** API Surface / Naming
- **Description:** `AgentRunner` interface has a comment "kept for backward compatibility" and overlaps with `swarm.AgentRunner` and `subagent.AgentRunner`. External callers might use the wrong one.
- **Suggested improvement:** Mark with `// Deprecated: Use ChatBridge instead.` Go convention.

### Finding 4.2 — A2A Security Type Deprecated but Still Exported (Low)
- **File:** `internal/a2a/types.go:177-183`
- **Severity:** Low
- **Category:** API Surface / Naming
- **Description:** `Security` struct has comment "Deprecated: Use SecurityScheme instead. Kept for backward compat." but no Go `// Deprecated:` doc comment. Linters and IDEs won't flag usage.
- **Suggested improvement:** Add `// Deprecated: Use SecurityScheme instead.` as a godoc comment.

### Finding 4.3 — Feishu Adapter Uses MessageId (Non-Go Idiom) in External Struct (Low)
- **File:** `internal/im/feishu_adapter.go:331`
- **Severity:** Low
- **Category:** Naming / Acronym Consistency
- **Description:** `msg.MessageId` uses `Id` instead of Go-standard `ID`. This is likely from an external API JSON tag, so it's acceptable for deserialization structs, but the Go field name should follow conventions.
- **Suggested improvement:** If this is a deserialization struct from external JSON, this is acceptable. If it's an internal type, rename to `MessageID`.

### Finding 4.4 — MCP ParseMessage Returns `interface{}` (Medium)
- **File:** `internal/mcp/jsonrpc.go:60`
- **Severity:** Medium
- **Category:** API Surface / Type Safety
- **Description:** `ParseMessage` returns `(interface{}, error)`, forcing every caller to type-assert. This is error-prone and produces verbose caller code (seen in `client.go:487`, `client.go:506`).
- **Suggested improvement:** Consider a sum type pattern:
  ```go
  type Message struct {
      Type     MessageType
      Request  *Request
      Response *Response
      Notification *Notification
  }
  ```

### Finding 4.5 — Inconsistent Event Type Representation (Low)
- **File:** `internal/swarm/manager.go:230` vs `internal/swarm/team.go:43-48`
- **Severity:** Low
- **Category:** Naming Consistency
- **Description:** Swarm `Event.Type` uses string constants (`"teammate_spawned"`, `"team_created"`) while `TeammateEvent.Type` uses typed int iota constants (`TeammateEventText`, `TeammateEventToolCall`). Similarly, `subagent.AgentEvent.Type` uses int iota. The mixed approach (strings vs ints) for event types in the same package is inconsistent.
- **Suggested improvement:** Use typed string constants for `Event.Type` as well, or use int iota for both.

---

## 5. API Surface Area

### Finding 5.1 — swarm.Manager.GetTaskManager Returns nil Without Error (Medium)
- **File:** `internal/swarm/manager.go:376-386`
- **Severity:** Medium
- **Category:** API Surface / Missing Validation
- **Description:** `GetTaskManager` returns `nil` when the team doesn't exist, with no error to distinguish "team not found" from "team has no task board". Callers must check for nil and guess the reason.
- **Suggested improvement:** Return `(*task.Manager, error)` like `EnsureTaskManager`, or at minimum document the nil semantics.

### Finding 5.2 — swarm.Manager.BroadcastToTeam Returns nil for Missing Team (Low)
- **File:** `internal/swarm/manager.go:329-354`
- **Severity:** Low
- **Category:** API Surface / Missing Validation
- **Description:** `BroadcastToTeam` returns `nil` (empty slice) when the team doesn't exist, making it impossible for callers to distinguish "no idle teammates" from "team not found".
- **Suggested improvement:** Return an error for unknown team, or document that nil means team not found.

### Finding 5.3 — swarm.Team.getTeammate and listTeammates Are Unexported but Operate on Exported Map (Low)
- **File:** `internal/swarm/team.go:205-220`
- **Severity:** Low
- **Category:** API Surface / Encapsulation
- **Description:** `Teammates` is an exported map (`map[string]*Teammate`), but access is intended through `getTeammate()` (unexported). External packages could directly mutate the map, bypassing mutex protection.
- **Suggested improvement:** Unexport the `Teammates` field and provide accessor methods.

### Finding 5.4 — Plugin Manager Exposes Mutatable Slices (Low)
- **File:** `internal/plugin/plugin.go:45-51`
- **Severity:** Low
- **Category:** API Surface / Encapsulation
- **Description:** `Plugins()` and `Results()` return internal slices that callers can mutate, potentially corrupting the Manager's state.
- **Suggested improvement:** Return copies of the slices.

### Finding 5.5 — tool.Registry.List Returns Unordered Results (Low)
- **File:** `internal/tool/tool.go:97-105`
- **Severity:** Low
- **Category:** API Surface / Non-determinism
- **Description:** `List()` iterates over a map, producing non-deterministic ordering. This means `ToDefinitions()` and `ToolNames()` produce different orderings across runs. While functionally correct, it can cause unnecessary LLM prompt variations and make testing harder.
- **Suggested improvement:** Sort the result by tool name before returning.

---

## Summary Table

| Severity | Count | Key Issues |
|----------|-------|------------|
| **High** | 3 | CommandTool placeholder (#1.1), operator precedence bug (#2.1), History[0] bounds check (#2.6) |
| **Medium** | 12 | ChatBridge/AgentRunner duplication, swallowed json.Marshal errors, MCP bare error returns, ParseMessage returns interface{}, adapter silent continue, DingTalk ignored marshals, truncation by bytes, GetTaskManager nil semantics |
| **Low** | 11 | Plugin.Init no-op, AgentRunner duplication, maxIter=0 undocumented, naming conventions, exported fields with intended internal access, unordered tool list, deprecated types without proper annotation |

**Top 3 Priority Fixes:**
1. **#2.1** — A2A client operator precedence bug: Will accept invalid auth tokens, a security-adjacent logic error.
2. **#1.1** — CommandTool placeholder: Silently returns fake results to the LLM.
3. **#2.6** — A2A handler History[0] access without bounds check: Potential index out of range panic in production.
