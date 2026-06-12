# Security Audit Report - ggcode

**Date**: 2025-07-14  
**Auditor**: Automated Security Review  
**Scope**: Full codebase security audit  

---

## Executive Summary

The ggcode project is a Go-based AI coding agent with TUI, daemon, WebUI, relay, IM gateway, and A2A protocol support. The codebase demonstrates several security-conscious design decisions (crypto/rand tokens, sandbox path checking, command gate, credential migration, constant-time API key comparison in A2A). However, several findings require attention across authentication, credential handling, and network security.

---

## CRITICAL

### C-01: WebUI Auth Token Comparison Vulnerable to Timing Attacks
- **File**: `internal/webui/auth.go:35,42`
- **Type**: CWE-208 (Observable Timing Discrepancy)
- **Description**: The `requireAuth` middleware compares the auth token using standard Go string equality (`==`) rather than constant-time comparison:
  ```go
  if strings.TrimPrefix(auth, "Bearer ") == s.authToken {  // line 35
  if r.URL.Query().Get("token") == s.authToken {           // line 42
  ```
- **Risk**: An attacker on the same network can use timing side-channels to brute-force the auth token byte-by-byte. Since the WebUI listens on `127.0.0.1` by default, this is exploitable only from the local machine or via DNS rebinding.
- **Mitigation**: Use `crypto/subtle.ConstantTimeCompare()` (already imported and used correctly in `internal/a2a/server.go:234`):
  ```go
  if subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(auth, "Bearer ")), []byte(s.authToken)) == 1 {
  ```

### C-02: DingTalk Access Token Logged in Cleartext
- **File**: `internal/im/dingtalk_adapter.go:512`
- **Type**: CWE-532 (Insertion of Sensitive Information into Log File)
- **Description**: When the DingTalk access token response fails to parse, the entire raw response body (containing the `accessToken`) is logged:
  ```go
  debug.Log("dingtalk", "adapter=%s token response: %s", a.name, string(data))
  ```
  Additionally at line 526, the `appKey` (application credential) is logged:
  ```go
  debug.Log("dingtalk", "adapter=%s token refreshed (appKey=%s), expires in %ds", a.name, a.appKey, expire)
  ```
- **Risk**: If debug logging is enabled, sensitive DingTalk credentials (accessToken and appKey) are written to log files, potentially exposing them to anyone with log access.
- **Mitigation**: Mask or omit sensitive values from log output. Log only success/failure status and expiration time.

---

## HIGH

### H-01: Relay Server WebSocket Allows All Origins (CORS Bypass)
- **File**: `ggcode-relay/main.go:590`
- **Type**: CWE-942 (Overly Permissive Cross-Origin Whitelist)
- **Description**: The WebSocket upgrader accepts connections from any origin:
  ```go
  var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
  ```
- **Risk**: Any malicious webpage can establish a WebSocket connection to the relay server, potentially intercepting or injecting messages if the token can be obtained (e.g., via logs).
- **Mitigation**: Restrict allowed origins to known domains or implement proper origin validation.

### H-02: Relay Token in URL Query Parameter
- **File**: `ggcode-relay/main.go:594`
- **Type**: CWE-598 (Use of GET Request Method With Sensitive Query Strings)
- **Description**: The authentication token is passed as a URL query parameter (`?token=...`), which means it appears in:
  - HTTP access logs
  - Browser history
  - Proxy logs
  - Referer headers
- **Risk**: Token exposure through server logs and proxy infrastructure.
- **Mitigation**: Pass the token in the WebSocket handshake via a custom header or subprotocol. At minimum, ensure relay access logs redact query parameters.

### H-03: Relay Token Partial Exposure in Logs
- **File**: `ggcode-relay/main.go:651`
- **Type**: CWE-532 (Insertion of Sensitive Information into Log File)
- **Description**: The first 8 characters of the relay token are logged:
  ```go
  log.Printf("[relay] %s connected: room=%s clients=%d", role, token[:8], len(rm.clients))
  ```
- **Risk**: With 8 hex characters exposed, the keyspace for brute-forcing the remaining characters is significantly reduced. For a 48-char hex token, this reduces entropy from ~192 bits to ~160 bits, which is still strong, but the partial leak is a security hygiene issue.
- **Mitigation**: Log only a hash of the token or the first 4 characters (like GitHub's token display).

### H-04: No CORS Configuration on WebUI Server
- **File**: `internal/webui/server.go`
- **Type**: CWE-346 (Origin Validation Error)
- **Description**: The WebUI HTTP server has no CORS headers configured. While this defaults to same-origin-only, the absence of explicit CORS policy combined with the token-in-URL-hash authentication pattern creates a risk if the SPA is served from a different origin in deployment.
- **Risk**: Potential cross-origin request issues; no explicit defense against CSRF via cross-origin framing.
- **Mitigation**: Add explicit CORS headers restricting allowed origins. Consider adding CSRF tokens for state-changing operations.

### H-05: A2A Push Notification SSRF
- **File**: `internal/a2a/server.go:772-793`
- **Type**: CWE-918 (Server-Side Request Forgery)
- **Description**: The `firePushNotifications` method sends HTTP POST requests to user-provided URLs without any validation or restriction:
  ```go
  url := cfg.URL  // user-controlled
  req, err := http.NewRequest("POST", url, bytes.NewReader(body))
  resp, err := http.DefaultClient.Do(req)
  ```
- **Risk**: An authenticated attacker can register push notification URLs pointing to internal services (e.g., `http://169.254.169.254/latest/meta-data/` for cloud metadata, or `http://localhost:PORT/admin`), causing the server to make arbitrary HTTP requests.
- **Mitigation**: Validate push notification URLs against an allowlist, block private/internal IP ranges, or use a dedicated HTTP client with network restrictions.

### H-06: keys.env Credentials Stored in Plaintext
- **File**: `internal/config/api_keys.go:536`
- **Type**: CWE-312 (Cleartext Storage of Sensitive Information)
- **Description**: API keys are stored in `~/.ggcode/keys.env` as plaintext shell exports:
  ```go
  fmt.Fprintf(&b, "export %s='%s'\n", k, existing[k])
  ```
  While the file is created with `0600` permissions (owner-only), the values are stored unencrypted.
- **Risk**: Any process running as the same user or root can read all stored API keys. Backup tools, file indexing, or accidental inclusion in dotfile repos could expose them.
- **Mitigation**: Consider using the OS keychain (macOS Keychain, Linux Secret Service) or encrypting the keys.env file with a user-provided passphrase. At minimum, warn users about the storage model.

---

## MEDIUM

### M-01: Plugin CommandTool Uses strings.Fields for Argument Splitting
- **File**: `internal/plugin/plugin.go:114`
- **Type**: CWE-78 (OS Command Injection)
- **Description**: User-provided arguments are split with `strings.Fields()` and appended to command arguments:
  ```go
  args = append(args, strings.Fields(params.Args)...)
  cmd := exec.CommandContext(ctx, c.execute, args...)
  ```
  While `exec.Command` does not invoke a shell (which prevents classic shell injection), `strings.Fields` splits on whitespace and does not handle quoted arguments, which could lead to unexpected argument splitting. The use of `exec.Command` with separate args (not shell) is good practice and limits injection.
- **Risk**: Low - argument splitting inconsistencies rather than actual injection, since `exec.Command` does not use a shell.
- **Mitigation**: Use proper shell-like argument parsing if the intent is to support quoted arguments, or document that Args is a simple space-separated string.

### M-02: Tunnel Crypto Derives Key Without Proper KDF
- **File**: `internal/tunnel/crypto.go:28-35`
- **Type**: CWE-328 (Use of Weak Hash)
- **Description**: When the token is shorter than 16 bytes, a 32-byte key is derived using Argon2id with a static salt:
  ```go
  var salt [16]byte  // all zeros
  derived := argon2.IDKey(key, salt[:], 1, 64*1024, 4, 32)
  ```
  The static (zero-value) salt weakens the KDF because identical tokens will produce identical keys. However, for tokens >= 16 bytes (the common case), the raw bytes are used directly as the AES key.
- **Risk**: Low in practice since tokens are typically 48 hex chars (24 bytes). The static salt reduces the effectiveness of the KDF for short tokens.
- **Mitigation**: Use a non-static salt derived from the token itself or a configurable pepper.

### M-03: Sandbox Path Check Can Be Nil (No Enforcement)
- **File**: `internal/tool/builtin.go:8-14`
- **Type**: CWE-863 (Incorrect Authorization)
- **Description**: When `policy` is nil, the `sandboxFor` function returns nil, and file operation tools skip path validation:
  ```go
  if policy == nil {
      return nil  // no sandbox check
  }
  ```
  Tools like `ReadFile`, `WriteFile`, etc., check `if t.SandboxCheck != nil` before enforcing restrictions.
- **Risk**: In configurations without a permission policy, the agent can read/write arbitrary files on the filesystem. This is by design (user opt-in), but it means that in daemon mode with no `allowed_dirs` configured, the sandbox is effectively disabled.
- **Mitigation**: Document this behavior prominently. Consider defaulting to the working directory when no policy is set.

### M-04: .gitignore Does Not Cover keys.env
- **File**: `.gitignore`
- **Type**: CWE-312 (Cleartext Storage of Sensitive Information)
- **Description**: The `.gitignore` file does not explicitly list `keys.env`. While `keys.env` is stored in `~/.ggcode/` (outside the repo), if a user accidentally creates it in the project directory, it could be committed. The `.ggcode/` directory IS gitignored, which covers instance-level keys.env.
- **Risk**: Potential accidental commit of API keys if a user creates keys.env in the project root.
- **Mitigation**: Add `keys.env` to `.gitignore` as a safety net.

### M-05: WebUI Token Passed in URL Fragment
- **File**: `internal/webui/server_static.go:13-35`
- **Type**: CWE-200 (Exposure of Sensitive Information)
- **Description**: The WebUI SPA extracts the auth token from the URL hash fragment and injects it into all API requests. While URL fragments are not sent to the server, they may be visible in:
  - Browser extensions
  - Shared screen sessions
  - Browser history (though fragments are typically not stored)
  
  The token is removed from the URL after extraction (`history.replaceState`), which is good.
- **Risk**: Low - the implementation correctly uses `history.replaceState` to remove the token from the URL bar. The token is only visible briefly during initial page load.
- **Mitigation**: Current implementation is adequate. Consider offering an alternative authentication flow via a login form.

### M-06: No Rate Limiting on Authentication Endpoints
- **Files**: `internal/webui/auth.go`, `ggcode-relay/main.go:592`, `internal/a2a/server.go`
- **Type**: CWE-307 (Improper Restriction of Excessive Authentication Attempts)
- **Description**: No rate limiting is implemented on any authentication endpoint:
  - WebUI REST API and WebSocket
  - Relay WebSocket connections
  - A2A JSON-RPC
- **Risk**: An attacker can brute-force auth tokens without being throttled or locked out.
- **Mitigation**: Implement rate limiting (e.g., per-IP token bucket) on authentication endpoints. For the relay, add exponential backoff for failed connection attempts.

### M-07: Relay Server No TLS
- **File**: `ggcode-relay/main.go:689`
- **Type**: CWE-319 (Cleartext Transmission of Sensitive Information)
- **Description**: The relay server only supports plain HTTP (`http.ListenAndServe`). WebSocket connections and all data (including encrypted tunnel messages, auth tokens) are transmitted in cleartext.
- **Risk**: Network-level eavesdroppers can intercept relay traffic, including the authentication tokens in query parameters.
- **Mitigation**: Add TLS support (configurable cert/key paths) or recommend deployment behind a TLS-terminating reverse proxy.

### M-08: A2A Agent Card Exposed Without Authentication
- **File**: `internal/a2a/server.go:93-94,185-192`
- **Type**: CWE-200 (Exposure of Sensitive Information)
- **Description**: The `/.well-known/agent.json` and `/.well-known/a2a.json` endpoints are served without authentication (`a2aMiddleware` only adds headers, doesn't check auth). The agent card includes the server URL and capabilities.
- **Risk**: Information disclosure about the A2A server's capabilities and network location. The agent card is designed to be public per the A2A specification, but exposing the URL on LAN without auth could aid reconnaissance.
- **Mitigation**: This is per A2A protocol specification. Ensure the agent card does not leak sensitive metadata. The current implementation appropriately limits the card to non-sensitive fields.

---

## LOW

### L-01: Token Passed as Query Parameter in WebUI WebSocket
- **File**: `internal/webui/server_static.go:29`
- **Type**: CWE-598 (Use of GET Request Method With Sensitive Query Strings)
- **Description**: The auth token is appended as a query parameter for WebSocket connections:
  ```javascript
  if(url.indexOf('?')===-1)url+='?token='+t;else url+='&token='+t;
  ```
  This is necessary for WebSocket upgrade (which doesn't support custom headers in browser APIs), and the WebUI only listens on localhost.
- **Risk**: Low - localhost-only binding limits exposure. The token appears in server access logs if enabled.
- **Mitigation**: Current approach is acceptable for localhost-only usage.

### L-02: Example Config Contains Placeholder Secrets
- **File**: `README.md:497`, `ggcode.example.yaml` (referenced)
- **Type**: CWE-798 (Use of Hard-coded Credentials)
- **Description**: The README contains example configurations with placeholder API keys like `"my-secret-key"`. These are clearly examples, not real credentials.
- **Risk**: Negligible - clearly labeled as examples.
- **Mitigation**: No action needed. The current approach of using obvious placeholders is fine.

### L-03: run_command Uses Shell Execution
- **File**: `internal/tool/run_command.go:232`
- **Type**: CWE-78 (OS Command Injection)
- **Description**: Commands are executed through a shell (`/bin/bash -c` or `/bin/sh -c` via `util.NewShellCommandContext`). However, comprehensive mitigations are in place:
  - **Command Gate** (`NewCommandGate`) blocks destructive commands (rm -rf /, etc.)
  - **Permission system** requires user confirmation for dangerous commands in supervised mode
  - **Working directory** is locked to the project root (LLM-provided working_dir is ignored)
  - **Timeout** prevents indefinite execution
- **Risk**: The shell execution is by design (the agent needs to run arbitrary build/test commands). The defense-in-depth approach (gate + permission + sandbox) mitigates the inherent risk.
- **Mitigation**: Current mitigations are appropriate. The command gate should be regularly updated to cover new attack patterns.

### L-04: OAuth Token Cache File Permissions
- **File**: `internal/config/api_keys.go:543,547`
- **Type**: CWE-732 (Incorrect Permission Assignment for Critical Resource)
- **Description**: The `keys.env` file is created with `0600` permissions and then explicitly chmod'd to ensure correct permissions even with restrictive umasks. This is good practice.
- **Risk**: Negligible - correct permissions are enforced.
- **Mitigation**: No action needed.

### L-05: crypto/rand Used for Token Generation
- **File**: `internal/webui/auth.go:11-17`
- **Type**: N/A (Positive Finding)
- **Description**: Auth tokens are generated using `crypto/rand` (32 bytes), which is cryptographically secure. This is the correct approach.
- **Risk**: None.

### L-06: Argon2id Used for Key Derivation
- **File**: `internal/tunnel/crypto.go:31`
- **Type**: N/A (Positive Finding)
- **Description**: Short tokens are strengthened using Argon2id, which is the recommended KDF. Parameters (t=1, m=64KB, p=4) are conservative but adequate for the use case.
- **Risk**: None (see M-02 for the static salt concern).

---

## Summary Table

| ID  | Severity | Component | Issue |
|-----|----------|-----------|-------|
| C-01 | Critical | WebUI Auth | Timing attack on token comparison |
| C-02 | Critical | IM/DingTalk | Access token logged in cleartext |
| H-01 | High | Relay | WebSocket allows all origins |
| H-02 | High | Relay | Token in URL query parameter |
| H-03 | High | Relay | Token prefix logged |
| H-04 | High | WebUI | No CORS configuration |
| H-05 | High | A2A | Push notification SSRF |
| H-06 | High | Config | Plaintext API key storage |
| M-01 | Medium | Plugin | Argument splitting inconsistency |
| M-02 | Medium | Tunnel | Static salt in KDF |
| M-03 | Medium | Tools | Nil sandbox bypass |
| M-04 | Medium | Config | keys.env not in .gitignore |
| M-05 | Medium | WebUI | Token in URL fragment |
| M-06 | Medium | Auth | No rate limiting |
| M-07 | Medium | Relay | No TLS |
| M-08 | Medium | A2A | Agent card unauthenticated |
| L-01 | Low | WebUI | WebSocket token in query |
| L-02 | Low | Docs | Placeholder secrets |
| L-03 | Low | Tools | Shell execution (mitigated) |
| L-04 | Low | Config | Token file permissions (good) |
| L-05 | Low | WebUI | Uses crypto/rand (good) |
| L-06 | Low | Tunnel | Uses Argon2id (good) |

---

## Positive Security Findings

1. **A2A uses constant-time comparison** (`crypto/subtle.ConstantTimeCompare`) for API key validation
2. **Comprehensive command gate** blocks destructive shell commands
3. **Permission modes** provide defense-in-depth for tool execution
4. **Sandbox path checking** restricts file operations to allowed directories
5. **Automatic plaintext key migration** from YAML to env references
6. **keys.env uses 0600 permissions** with explicit chmod
7. **Config API sanitizes API keys** (`***` masking in `sanitizeMap`)
8. **OAuth client secrets** are masked (`has_secret: true`) in API responses
9. **AES-GCM** used for tunnel encryption (authenticated encryption)
10. **MaxBytesReader** limits A2A request body size (4 MiB cap)
11. **Token hashing** in relay SQLite (SHA-256 of tokens stored, not plaintext)

---

## Recommendations (Priority Order)

1. **Fix C-01**: Replace `==` with `subtle.ConstantTimeCompare` in `internal/webui/auth.go`
2. **Fix C-02**: Remove accessToken and appKey from debug.Log output in DingTalk adapter
3. **Fix H-05**: Add URL validation/allowlist for A2A push notification targets
4. **Fix H-01**: Restrict relay WebSocket origin checking
5. **Fix H-03**: Reduce or eliminate token prefix in relay logs
6. **Address H-06**: Document keys.env storage model; consider OS keychain integration
7. **Address M-06**: Implement basic rate limiting on auth endpoints
8. **Address M-07**: Add TLS support to relay server or document reverse proxy requirement
9. **Address M-04**: Add `keys.env` to `.gitignore`
10. **Address M-03**: Default sandbox to working directory when no policy is set
