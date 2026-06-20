# ggcode Security Review Report

**Reviewer**: Security Engineer (Automated Audit)  
**Date**: 2025-07-14  
**Scope**: Authentication, Key Management, Permissions, Input Validation, Command Execution, Network Exposure, Dependencies

---

## Executive Summary

The ggcode project demonstrates a **mature security posture** for a developer-tooling codebase. It implements multiple authentication methods (API key, OAuth2/OIDC, mTLS), a sandbox model with symlink resolution, SSRF protections, and a layered command gate. However, several medium-to-high severity issues were identified that warrant remediation, particularly around config file permissions, JWT validation fallback, WebSocket origin checking, and the bypass/autopilot permission modes.

**Overall Security Score: 7.0 / 10**

---

## Dimension Scores

| # | Dimension | Score | Summary |
|---|-----------|-------|---------|
| 1 | Authentication & Authorization | 7/10 | Multi-auth stack is well-implemented; JWT fallback and localhost bypass need attention |
| 2 | Key Management | 6/10 | Token cache uses 0600; config file stores API keys at 0644; env expansion is safe |
| 3 | Permission Modes | 7/10 | Sandbox resolves symlinks correctly; bypass/autopilot modes are dangerous by design |
| 4 | Input Validation | 6/10 | JSON schema enforced; WebSocket lacks message size limits; command gate is regex-based |
| 5 | Command Execution | 7/10 | Command gate blocks catastrophic patterns; run_command uses exec.Command (no shell); regex bypass possible |
| 6 | Network Exposure | 5/10 | WebUI has no TLS; WebSocket allows all origins; A2A localhost-only by default is good |
| 7 | Dependency Security | 7/10 | Go module dependencies are reasonably current; no critical known CVEs in direct deps |

---

## Detailed Findings

### CRITICAL

*(None found)*

### HIGH

#### H-01: JWT Validation Fallback Bypasses Issuer/Audience Checks
- **File**: `internal/auth/a2a_oauth.go:442-455`
- **Description**: When `jwt.ParseWithClaims` fails due to issuer/audience mismatch but the token signature is valid, the code falls back to parsing claims manually and returning them. This means a token signed by a valid key but for a different audience/issuer will be accepted.
- **Impact**: A token issued for a different application could be used to authenticate against the A2A server.
- **Recommendation**: Remove the fallback or make it opt-in with explicit config. Issuer and audience should always be strictly validated.

#### H-02: Config File Stores API Keys at 0644 Permissions
- **File**: `internal/config/config_save.go:62`
- **Description**: `ggcode.yaml` is written with `0644` permissions (world-readable). This file contains API keys for LLM providers, MCP server credentials, and potentially OAuth client secrets.
- **Impact**: Any local user can read the config file and extract API keys and secrets.
- **Recommendation**: Write config files with `0600` permissions. Add a warning at startup if the config file is group/world-readable.

#### H-03: WebSocket Upgrader Allows All Origins (CSRF via WebSocket)
- **File**: `internal/webui/server_websocket.go:16-18`
- **Description**: `CheckOrigin` returns `true` unconditionally. Any website can establish a WebSocket connection to the WebUI, which runs on `127.0.0.1` but with a random port. If an attacker can guess or discover the port (e.g., via timing attacks or process listing), they can send commands as the authenticated user.
- **Impact**: Cross-site WebSocket hijacking (CSWSH) enables arbitrary command execution through the agent.
- **Recommendation**: Validate the `Origin` header against the server's own address. At minimum, check that the Origin matches `localhost`/`127.0.0.1`.

#### H-04: WebSocket Messages Have No Size Limit
- **File**: `internal/webui/server_websocket.go:128-134`
- **Description**: There is no `SetReadLimit` on the WebSocket connection. An attacker with WebSocket access can send arbitrarily large messages, potentially causing OOM. Base64-encoded file attachments are decoded without size validation.
- **Impact**: Denial of service via memory exhaustion.
- **Recommendation**: Call `conn.SetReadLimit()` with a reasonable cap (e.g., 10 MB). Validate decoded base64 data sizes before processing.

### MEDIUM

#### M-01: Auth Store Directory Created with 0755 Permissions
- **File**: `internal/auth/store.go:146`
- **Description**: The auth store directory (`~/.ggcode/oauth-tokens/`) is created with `0755`. While individual token files are written at `0600`, the directory being world-readable/traversable may leak filenames (which contain provider and client ID info).
- **Impact**: Minor information disclosure of OAuth provider names and client IDs.
- **Recommendation**: Create the directory with `0700` permissions.

#### M-02: Token Cache Directory Created with 0755 Permissions
- **File**: `internal/auth/a2a_token_cache.go:62` (approximately)
- **Description**: Same as M-01 for the token cache directory.
- **Recommendation**: Use `0700` for the directory.

#### M-03: Command Gate Uses Regex-Based Detection (Bypassable)
- **File**: `internal/tool/command_gate.go:60-124`
- **Description**: Dangerous command detection relies on regex pattern matching. This is inherently fragile -- encoding tricks, shell variable expansion (`$()`, backticks, env vars), pipe obfuscation, and multi-line commands can bypass regex checks.
- **Impact**: A sophisticated LLM prompt injection could craft commands that bypass the gate.
- **Recommendation**: Supplement regex with AST-based shell parsing (e.g., using `mvdan/sh`). Block commands that cannot be fully parsed. Consider using a shell sandbox (e.g., `nsjail`, `bwrap`) for execution isolation.

#### M-04: run_command Executes Arbitrary Commands Without Path Restriction
- **File**: `internal/tool/run_command.go:108-160`
- **Description**: `run_command` has no `working_dir` restriction from the sandbox. While the `RunCommand` struct has a `WorkingDir` field, the command itself can `cd` anywhere. The command gate is the only protection layer.
- **Impact**: In bypass/autopilot mode, the agent can execute arbitrary commands with full user privileges.
- **Recommendation**: This is somewhat by-design, but document the risk clearly. Consider adding `allowed_commands` allowlist as a config option for restrictive environments.

#### M-05: LLM Output Not Sanitized Before Tool Execution
- **File**: `internal/agent/agent_tool.go` (tool execution flow)
- **Description**: The LLM's tool call arguments (e.g., file paths, command strings, URLs) are passed directly to tools with only JSON schema validation. There is no sanitization layer for LLM-generated content before it reaches tools.
- **Impact**: Prompt injection via external content (web_fetch results, file contents) could trick the LLM into generating malicious tool arguments.
- **Recommendation**: Add a sanitization/validation layer between LLM output and tool execution. At minimum, re-validate paths against sandbox and re-check commands against the gate.

#### M-06: Auth Token in URL Query Parameters (WebUI)
- **File**: `internal/webui/auth.go:25-34`
- **Description**: The auth token can be provided via URL query parameter (`?token=...`). This means the token may appear in browser history, server logs, and Referer headers.
- **Impact**: Token leakage through logs or browser history.
- **Recommendation**: Prefer Authorization header or cookie-based token delivery. If query params are needed for simplicity, document the risk and consider short-lived tokens.

#### M-07: No TLS for WebUI HTTP Server
- **File**: `internal/webui/server.go:254`
- **Description**: The WebUI HTTP server runs plain HTTP (`http.Serve`). When bound to `0.0.0.0` (in daemon mode with A2A auth), traffic including the auth token and chat content is transmitted in cleartext.
- **Impact**: Network-level eavesdropping on agent conversations and configuration.
- **Recommendation**: Add optional TLS support for the WebUI. Consider reusing the A2A mTLS cert/key configuration.

### LOW

#### L-01: A2A Allow Unauthenticated Config Flag
- **File**: A2A config `allow_unauthenticated`
- **Description**: An explicit config option allows disabling authentication entirely. While documented as defaulting to `false`, its existence is a footgun.
- **Recommendation**: Warn loudly at startup when this is enabled.

#### L-02: No Rate Limiting on WebUI or A2A Endpoints
- **File**: `internal/webui/server.go`, `internal/a2a/server.go`
- **Description**: No rate limiting on authentication attempts. An attacker can brute-force the auth token.
- **Recommendation**: Add exponential backoff or rate limiting on auth failures.

#### L-03: Error Messages May Leak Internal Paths
- **File**: Multiple tools in `internal/tool/`
- **Description**: Error messages sometimes include full file paths (e.g., `fmt.Sprintf("error accessing file: %v", err)`). This could leak server directory structure.
- **Recommendation**: Sanitize paths in error messages returned to the user.

#### L-04: No Content Security Policy (CSP) for WebUI SPA
- **File**: `internal/webui/server_static.go`
- **Description**: The SPA is served without CSP headers, making it vulnerable to XSS if any user content is rendered without escaping.
- **Recommendation**: Add strict CSP headers to all WebUI responses.

#### L-05: PKCE Code Verifier Exposed via Public Function
- **File**: `internal/auth/pkce.go:4`
- **Description**: `GenerateCodeVerifier()` is exported, though it should only be used internally. This is minor but increases the attack surface.
- **Recommendation**: Make PKCE helpers unexported unless needed externally.

#### L-06: Env Expansion Uses Regex (Safe but Fragile)
- **File**: `internal/config/env.go:25`
- **Description**: The `${VAR}` expansion pattern is regex-based. While currently safe (no nested expansion), adding nested expansion or command substitution would be dangerous.
- **Recommendation**: Document that nested expansion must never be supported. Add a comment near the regex.

---

## Security Strengths

1. **SSRF Protection in web_fetch**: Comprehensive private IP blocking with DNS-level resolution (`resolvePublicDialAddress`), fail-closed behavior, and coverage of IPv4-mapped IPv6 addresses.
2. **Constant-Time API Key Comparison**: A2A server uses `crypto/subtle.ConstantTimeCompare` for API key validation.
3. **Sandbox Symlink Resolution**: `PathSandbox` resolves symlinks using `filepath.EvalSymlinks` to prevent symlink-based sandbox escapes.
4. **Atomic File Writes**: Config and auth store use atomic write (write to temp + rename) to prevent corruption.
5. **Token Cache Isolation**: OAuth tokens cached per `{provider}-{clientID}` with `0600` permissions prevents cross-instance overwrites.
6. **Config Sanitization for API**: WebUI sanitizes API keys, OAuth secrets, and MCP env vars before returning config to the browser.
7. **Body Size Limits**: A2A server limits request body to 4 MiB; web_fetch limits response to 10 MiB.
8. **Multi-Auth A2A**: Support for simultaneous api_key, OAuth2, OIDC, and mTLS authentication.
9. **PKCE for OAuth2**: Proper PKCE implementation with S256 code challenge.
10. **Command Gate Patterns**: Comprehensive regex patterns for detecting dangerous commands (rm -rf /, kill security tools, overwrite system files).

---

## Priority Remediation Plan

### Phase 1: Immediate (1-2 weeks)

| Priority | Finding | Action |
|----------|---------|--------|
| P1 | H-02 | Change config file permissions to `0600` |
| P1 | H-03 | Add Origin validation to WebSocket upgrader |
| P1 | H-04 | Add `conn.SetReadLimit()` to WebSocket connections |
| P1 | M-06 | Move auth token from query param to header |

### Phase 2: Short-term (2-4 weeks)

| Priority | Finding | Action |
|----------|---------|--------|
| P2 | H-01 | Remove JWT issuer/audience fallback or make opt-in |
| P2 | M-01, M-02 | Change auth/token directory permissions to `0700` |
| P2 | M-07 | Add optional TLS support for WebUI |
| P2 | L-02 | Add rate limiting on auth endpoints |

### Phase 3: Medium-term (1-2 months)

| Priority | Finding | Action |
|----------|---------|--------|
| P3 | M-03 | Evaluate AST-based shell parsing for command gate |
| P3 | M-05 | Add LLM output sanitization layer |
| P3 | L-04 | Add CSP headers to WebUI |
| P3 | M-04 | Document risk of unrestricted command execution |

### Phase 4: Long-term (2-4 months)

| Priority | Finding | Action |
|----------|---------|--------|
| P4 | -- | Add security fuzzing for tool inputs |
| P4 | -- | Implement optional container-based sandbox for command execution |
| P4 | -- | Add automated dependency vulnerability scanning (e.g., `govulncheck` in CI) |
| P4 | -- | Security audit of MCP bridge and remote tool execution path |

---

## Security Improvement Roadmap

```
Q3 2025:
  [x] Core auth stack (OAuth2, OIDC, mTLS, API key)
  [x] Path sandbox with symlink resolution
  [x] Command gate with catastrophic pattern blocking
  [x] SSRF protection in web_fetch
  [ ] Fix HIGH findings (H-01 through H-04)
  [ ] Fix M-01, M-02 (directory permissions)

Q4 2025:
  [ ] TLS for WebUI
  [ ] Rate limiting on auth endpoints
  [ ] AST-based command validation
  [ ] LLM output sanitization layer
  [ ] CSP headers for WebUI SPA

Q1 2026:
  [ ] Container-based command sandbox
  [ ] Automated vuln scanning in CI pipeline
  [ ] Security fuzz testing for tool inputs
  [ ] External penetration test
```

---

*This review was conducted via static analysis of the source code. Runtime testing and dynamic analysis would provide additional coverage.*
