# Security Review

**Date**: 2026-04-25  
**Reviewer**: ggcode security audit  
**Scope**: Full codebase (`internal/`, `cmd/`)

---

## Summary

The codebase demonstrates generally sound security practices for a developer tool. No critical vulnerabilities were identified. Several areas warrant attention for hardening.

| Severity | Count |
|----------|-------|
| High     | 0     |
| Medium   | 3     |
| Low      | 4     |
| Info     | 3     |

---

## Findings

### MEDIUM-1: OAuth callback server binds to all interfaces

**Files**: `internal/auth/claude_oauth.go:97`, `internal/mcp/oauth.go:519`

OAuth callback servers use `http.Server` with `ListenAndServe` on a port. While the callback is ephemeral and uses PKCE/state validation, the server should explicitly bind to `127.0.0.1` rather than `0.0.0.0` to prevent network-level callback interception.

**Recommendation**: Use `net.Listen("tcp", "127.0.0.1:0")` instead of `:0`.

### MEDIUM-2: ACP/A2A HTTP server has no authentication

**File**: `internal/a2a/server.go:77-85`

The A2A server exposes `/.well-known/agent.json` and `/` RPC endpoints. While intended for local use, there is no authentication or token validation on incoming requests. Any local process can interact with the agent.

**Recommendation**: Add bearer token validation or restrict to Unix domain sockets.

### MEDIUM-3: Debug dump writes LLM request to temp directory

**File**: `internal/provider/retry.go:244`

On retry failures, the full LLM request (including API keys in headers) is written to `os.TempDir()/ggcode-<provider>-last-request.json`. This could expose credentials if the temp directory is shared.

**Recommendation**: Redact sensitive headers before writing, or write to `~/.ggcode/debug/` with restrictive permissions.

---

### LOW-1: IM example commands show placeholder secrets

**File**: `cmd/ggcode/im_cmd.go:432-435`

Help text shows `--extra app_secret=sss --extra token=xxx`. While clearly placeholders, users might copy-paste without changing values.

**Recommendation**: Use `<your_app_secret>` and `<your_token>` format for placeholders.

### LOW-2: Reflection-based WorkingDir sync

**File**: `internal/agent/agent_tool.go` (`syncToolWorkingDir`)

Uses `reflect.Value` to set `WorkingDir` on tool structs. While functional, reflection bypasses compile-time type safety. A misnamed field would silently fail.

**Recommendation**: Consider an explicit `WorkingDirSetter` interface as a complement, with a compile-time assertion.

### LOW-3: Shell command execution has no output size limit

**File**: `internal/tool/run_command.go`

`run_command` captures all stdout/stderr into memory. Malicious or accidental commands (e.g., `cat /dev/urandom`) could consume excessive memory.

**Recommendation**: Add a configurable output size limit (e.g., 10MB) with truncation.

### LOW-4: No rate limiting on permission approval prompts

**File**: `internal/permission/`

In supervised mode, rapid tool calls can flood the user with approval prompts. There is no debounce or rate limiting.

**Recommendation**: Batch consecutive approval requests with a short window.

---

### INFO-1: SQL injection not applicable

The codebase uses SQLite only in `internal/harness/` with parameterized queries via `modernc.org/sqlite`. No string-interpolated SQL was found.

### INFO-2: Secret scanning implemented

`internal/acp/handler.go:416` marks fields as secret, and the project has secret scanning infrastructure via MCP tools.

### INFO-3: Path sandboxing present

`internal/permission/` implements directory-based sandboxing (`allowed_dirs`), and file tools use `SandboxCheck` before operations.

---

## Recommendations

1. **Short-term**: Fix MEDIUM-1 (localhost binding) and MEDIUM-3 (debug dump redaction)
2. **Medium-term**: Add auth to A2A server (MEDIUM-2), add output size limits (LOW-3)
3. **Long-term**: Consider a formal threat model for network-exposed surfaces (IM gateway, A2A, MCP OAuth)

---

## Positive Observations

- **Permission system** is well-layered with 5 modes and per-tool policies
- **Environment variable expansion** (`${ENV_VAR}`) avoids hardcoding secrets in config
- **No SQL injection risk** — all DB access uses parameterized queries
- **Secret masking** in TUI display (`apikey=%s` with masked value)
- **Sandbox checks** on file operations prevent arbitrary filesystem access
- **Dangerous tool classification** in `internal/permission/dangerous.go`
