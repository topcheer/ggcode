# Round 8 Security Audit Report

**Date:** 2025-07-27
**Scope:** Full codebase — credential leakage, authentication, injection, timing attacks, permission escalation, secrets in source, cryptography, network security
**Codebase:** `github.com/topcheer/ggcode` (~149k LOC non-test, 488 Go source files)

---

## Executive Summary

The codebase demonstrates a strong security posture overall, with several well-designed mechanisms:
- A2A server uses `subtle.ConstantTimeCompare` for API key validation
- Token cache files use 0600 permissions with forced `os.Chmod`
- Plaintext API key migration system auto-migrates secrets to `${ENV_VAR}` references
- PKCE code verifiers and OAuth state use `crypto/rand`
- Config API sanitization strips API keys before serving to WebUI
- No `InsecureSkipVerify: true` found anywhere in the codebase
- Path sandbox enforces `filepath.Clean` + prefix checks

**However**, several findings require attention:

| Severity | Count |
|----------|-------|
| Critical | 2 |
| High | 4 |
| Medium | 6 |
| Low | 5 |

---

## CRITICAL Findings

### C-1: ggcode-relay WebSocket Zero Authentication

**File:** `ggcode-relay/relay.go:611-617`
**Severity:** Critical

```go
var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

func (h *hub) handleWS(w http.ResponseWriter, r *http.Request) {
    conn, err := upgrader.Upgrade(w, r, nil)
    // ...
    token := r.URL.Query().Get("token")
    role := r.URL.Query().Get("role")
```

**Attack Scenario:** The relay server accepts WebSocket connections from any origin with `CheckOrigin: return true`. The `token` query parameter is a room identifier, NOT an authentication token — any client connecting with `?role=client&token=<room>` can join any room and read/write all messages. There is no authentication mechanism between the relay server and connecting clients.

**Impact:** Full session hijacking. An attacker who can reach the relay server (exposed on `:`+port) can:
- Join any workspace room and read all conversation data
- Inject malicious messages into active sessions
- Exfiltrate code, prompts, and tool outputs

**Recommended Fix:**
1. Implement shared-secret authentication between desktop and relay
2. Validate `token` as a cryptographic session token (not a room name)
3. Set proper `CheckOrigin` policy
4. Add TLS support for the relay listener

---

### C-2: WebUI WebSocket CheckOrigin Always Returns True

**File:** `internal/webui/server_websocket.go:16-17`
**Severity:** Critical

```go
var upgrader = websocket.Upgrader{
    CheckOrigin: func(r *http.Request) bool { return true },
}
```

**Attack Scenario:** The WebUI WebSocket accepts connections from any origin. Although a Bearer token is required for initial HTTP endpoints, the WebSocket upgrade does not validate the `Origin` header. An attacker can create a malicious webpage that opens a WebSocket connection to `ws://localhost:<port>/ws` (if they know the port).

**Impact:** Cross-Site WebSocket Hijacking (CSWSH). If a user visits an attacker-controlled page while the WebUI is running:
- The attacker page can establish a WebSocket using the token embedded in the SPA URL
- Read all conversation messages (including tool outputs that may contain secrets)
- Inject messages as the user

**Recommended Fix:**
```go
var upgrader = websocket.Upgrader{
    CheckOrigin: func(r *http.Request) bool {
        origin := r.Header.Get("Origin")
        return origin == "" || strings.HasPrefix(origin, "http://localhost") || strings.HasPrefix(origin, "http://127.0.0.1")
    },
}
```

---

## HIGH Findings

### H-1: WebUI Auth Token Uses Non-Constant-Time Comparison

**File:** `internal/webui/auth.go:39-44`
**Severity:** High

```go
func (s *Auth) Middleware(next http.HandlerFunc) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        if !s.ValidateRequest(r) {
            http.Error(w, "unauthorized", http.StatusUnauthorized)
            return
        }
        next(w, r)
    }
}
```

Where `ValidateRequest` at line 42:
```go
if r.URL.Query().Get("token") == s.authToken {
```

And at line 39:
```go
if authHeader == "Bearer "+s.authToken {
```

**Attack Scenario:** The WebUI authentication token is compared using standard string equality (`==`), which short-circuits on the first mismatched byte. An attacker with network access to localhost can measure response times to progressively guess the token character by character.

**Impact:** Authentication bypass via timing side-channel. The token is 32 hex characters (128 bits), so practical exploitation is difficult but not impossible — automated timing attacks on localhost can achieve high resolution.

**Recommended Fix:**
```go
import "crypto/subtle"

// In ValidateRequest:
if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("token")), []byte(s.authToken)) == 1 {
    return true
}
authHeader := r.Header.Get("Authorization")
if subtle.ConstantTimeCompare([]byte(authHeader), []byte("Bearer "+s.authToken)) == 1 {
    return true
}
```

---

### H-2: DingTalk Error Messages Expose Full API Response Body

**File:** `internal/im/dingtalk_adapter.go:512-513`
**Severity:** High

```go
debug.Log("dingtalk", "adapter=%s token response: %s", a.name, string(data))
return fmt.Errorf("DingTalk accessToken is empty: %s", string(data))
```

And at line 577-578:
```go
debug.Log("dingtalk", "adapter=%s streamOpen response: %s", a.name, string(data))
return "", fmt.Errorf("DingTalk stream endpoint/ticket empty: %s", strings.TrimSpace(string(data)))
```

And at line 581-582:
```go
wsURL := fmt.Sprintf("%s?ticket=%s", endpoint, ticket)
debug.Log("dingtalk", "adapter=%s wsURL=%s", a.name, wsURL)
```

**Attack Scenario:** When DingTalk API calls fail, the full HTTP response body is logged via `debug.Log` and included in error messages. These response bodies may contain:
- Access tokens or partial tokens
- Session tickets
- Internal API error details
- Correlation IDs useful for further attacks

The `wsURL` including the `ticket` query parameter is logged in plaintext.

**Impact:** Credential exposure in debug logs. If debug logging is enabled (common during development/troubleshooting), sensitive DingTalk authentication material is written to logs.

**Recommended Fix:**
```go
debug.Log("dingtalk", "adapter=%s token response: (redacted, %d bytes)", a.name, len(data))
return fmt.Errorf("DingTalk accessToken is empty (response length: %d)", len(data))
```

---

### H-3: Config File Saved with World-Readable Permissions (0644)

**File:** `internal/config/config_save.go:62`
**Severity:** High

```go
if err := util.AtomicWriteFile(c.FilePath, data, 0644); err != nil {
    return err
}
```

And `internal/config/config_save.go:141`:
```go
if err := util.AtomicWriteFile(fp, updated, 0644); err != nil {
    return err
}
```

And `internal/config/config_save.go:388`:
```go
return os.WriteFile(path, data, 0644)
```

And `internal/config/instance.go:402`:
```go
if err := writeFileAtomic(path, data, 0644); err != nil {
```

**Attack Scenario:** The main config file `~/.ggcode/ggcode.yaml` is saved with mode 0644. While the codebase has a robust API key migration system that moves plaintext keys to `${ENV_VAR}` references and stores actual keys in `keys.env` (0600), there is a race window during the save-migrate cycle where API keys may be present in the YAML file with world-readable permissions. Additionally, if migration fails or is incomplete, keys remain exposed.

**Impact:** Any local user on the system can read the config file, potentially containing:
- A2A auth API keys (before migration completes)
- MCP server headers/tokens
- Vendor endpoint configurations

**Recommended Fix:** Change config file permissions to 0600:
```go
if err := util.AtomicWriteFile(c.FilePath, data, 0600); err != nil {
    return err
}
_ = os.Chmod(c.FilePath, 0600) // Force regardless of umask
```

---

### H-4: Session Temp File Created with Default Permissions

**File:** `internal/session/store.go:319`
**Severity:** High

```go
tmp := path + ".tmp"
f, err := os.Create(tmp)
```

**Attack Scenario:** `os.Create` uses mode 0666 (modified by umask, typically resulting in 0644). The session JSONL file contains complete conversation history including any code snippets, tool outputs, and potentially sensitive data that was discussed. During the write-rename cycle, the temp file is world-readable.

**Impact:** Local information disclosure. Any user on the system can read session data during the brief window the temp file exists.

**Recommended Fix:**
```go
f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
```

---

## MEDIUM Findings

### M-1: Relay Server Listens on All Interfaces Without TLS

**File:** `ggcode-relay/relay.go:781-783`
**Severity:** Medium

```go
log.Printf("[relay] listening on :%s", port)
if err := http.ListenAndServe(":"+port, mux); err != nil {
    log.Fatal(err)
}
```

**Attack Scenario:** The relay server binds to `0.0.0.0` by default with no TLS. All WebSocket communication (including session tokens, conversation data, and code) is transmitted in plaintext. Any network attacker can intercept or modify this data.

**Impact:** Man-in-the-middle attacks on relay communication. Data exfiltration of code and conversations.

**Recommended Fix:** Add TLS support with configurable certificate, or default to `127.0.0.1` binding.

---

### M-2: Tunnel Relay Client Passes Token in URL Query Parameter

**File:** `internal/tunnel/relay_client.go:95`
**Severity:** Medium

```go
url := fmt.Sprintf("%s/ws?role=server&token=%s", rc.relayURL, rc.token)
```

**Attack Scenario:** The authentication token is included as a URL query parameter. This token may be logged by:
- HTTP proxies
- Web server access logs
- Browser history (if accessed via browser)
- Referrer headers

The token is not URL-encoded, which could cause issues with special characters.

**Impact:** Token exposure through access logs, proxy logs, or referrer leakage.

**Recommended Fix:** Pass the token via WebSocket protocol (first message after connect) or use a header-based authentication mechanism. At minimum, URL-encode the token:
```go
url := fmt.Sprintf("%s/ws?role=server&token=%s", rc.relayURL, url.QueryEscape(rc.token))
```

---

### M-3: MCP OAuth Logs client_id in Exchange Request

**File:** `internal/mcp/oauth.go:595`
**Severity:** Medium

```go
debug.Log("mcp-oauth", "exchange_code server=%s endpoint=%s client_id=%s redirect_uri=%s", h.serverName, tokenEndpoint, clientID, redirectURI)
```

**Attack Scenario:** The OAuth client_id is logged, which while not strictly secret for public clients, may be sensitive for confidential clients. Combined with the redirect_uri, this gives an attacker useful information for constructing phishing URLs.

**Impact:** Information leakage useful for OAuth phishing attacks.

**Recommended Fix:** Redact or omit client_id from debug logs:
```go
debug.Log("mcp-oauth", "exchange_code server=%s endpoint=%s redirect_set=%v", h.serverName, tokenEndpoint, redirectURI != "")
```

---

### M-4: Worktree Tools Use math/rand for Name Generation

**File:** `internal/tool/worktree_tools.go:7`
**Severity:** Medium

```go
"math/rand"
```

**Attack Scenario:** Worktree name generation uses `math/rand` instead of `crypto/rand`. If worktree names need to be unpredictable (e.g., to prevent name collision attacks or guessing), the predictability of `math/rand` could be exploited.

**Impact:** Low practical impact — worktree names don't need to be cryptographically random. But it's a deviation from the pattern used elsewhere (the codebase overwhelmingly uses `crypto/rand`).

**Recommended Fix:** Use `crypto/rand` for consistency:
```go
import "crypto/rand"
// Generate random hex suffix
b := make([]byte, 4)
rand.Read(b)
```

---

### M-5: Token-in-URL Pattern for WebUI Authentication

**File:** `internal/webui/auth.go:23`, `internal/webui/server_static.go:29`
**Severity:** Medium

```go
// From auth.go:
// ?token=<token> query parameter (WebSocket, fallback)

// From server_static.go:
if(url.indexOf('?')===-1)url+='?token='+t;else url+='&token='+t;
```

**Attack Scenario:** The WebUI authentication token is passed in the URL fragment (`#token=...`) or query parameter. This token:
- May be logged by browser extensions
- Appears in browser history
- Could be leaked via Referer headers if the user navigates externally
- Is visible in the address bar

**Impact:** Token leakage through browser history, extensions, or referrer headers. Combined with C-2 (CheckOrigin bypass), this enables cross-origin attacks.

**Recommended Fix:**
- Use `#token=` (fragment) instead of `?token=` for HTTP requests (already partially done)
- Implement cookie-based authentication for subsequent requests
- Set short token expiry

---

### M-6: DingTalk AppKey Logged in Plaintext

**File:** `internal/im/dingtalk_adapter.go:526`
**Severity:** Medium

```go
debug.Log("dingtalk", "adapter=%s token refreshed (appKey=%s), expires in %ds", a.name, a.appKey, expire)
```

**Attack Scenario:** The DingTalk `appKey` is logged on every token refresh. While `appKey` is technically a public identifier (similar to OAuth client_id), logging it alongside adapter names creates an information disclosure vector.

**Impact:** Information leakage of DingTalk integration configuration.

**Recommended Fix:**
```go
debug.Log("dingtalk", "adapter=%s token refreshed, expires in %ds", a.name, expire)
```

---

## LOW Findings

### L-1: Test Files Contain Hardcoded API Keys

**Files:**
- `internal/a2a/multi_agent_test.go:35`: `const mAPIKey = "ggcode-a2a-test-key-2025"`
- `internal/a2a/e2e_test.go:27`: `const e2eAPIKey = "ggcode-a2a-test-key-2025"`

**Severity:** Low (test-only)

**Attack Scenario:** These are test-only constants that are not used in production. However, secret scanning tools may flag these as potential leaks, and copy-paste into production code is a risk.

**Impact:** False positives in secret scanning; minimal direct risk.

**Recommended Fix:** Use `test_api_key_` prefix or load from test environment variables. Add a `.secrets.baseline` file for scanning tools.

---

### L-2: No Rate Limiting on Authentication Endpoints

**Files:**
- `internal/a2a/server.go` (authenticate method)
- `internal/webui/auth.go` (Middleware)
- `ggcode-relay/relay.go` (handleWS)

**Severity:** Low

**Attack Scenario:** No rate limiting is applied to authentication attempts. An attacker with local network access could brute-force:
- A2A API keys (mitigated by constant-time comparison but not by rate limits)
- WebUI auth tokens (mitigated by token length but not by rate limits)

**Impact:** Brute-force attacks against authentication tokens are feasible given enough time, though the 32-byte hex tokens make this practically difficult.

**Recommended Fix:** Add simple rate limiting middleware (e.g., token bucket) for authentication endpoints.

---

### L-3: WebUI Binds to Random Port on localhost

**File:** `internal/webui/server.go` (as documented in AGENTS.md)
**Severity:** Low

The WebUI binds to `127.0.0.1:0` (random port). While this prevents remote access, any local process can connect. Combined with the WebSocket CheckOrigin issue (C-2), a malicious local application could interact with the WebUI.

**Recommended Fix:** Document the local-only binding as a security boundary. Consider adding a Unix socket option for stronger isolation.

---

### L-4: No Request Size Limit on Some WebSocket Messages

**File:** `internal/webui/server_websocket.go`
**Severity:** Low

The A2A server properly limits request bodies to 4 MiB (`http.MaxBytesReader`), but the WebUI WebSocket handler does not enforce message size limits on incoming WebSocket frames.

**Impact:** Potential memory exhaustion via large WebSocket messages.

**Recommended Fix:** Set `ReadLimit` on the WebSocket connection:
```go
conn.SetReadLimit(4 << 20) // 4 MiB
```

---

### L-5: Cost Manager Writes with 0644 Permissions

**File:** `internal/cost/manager.go:103`
**Severity:** Low

```go
if err := os.WriteFile(tmp, data, 0644); err != nil {
```

Cost tracking data includes token usage information. While not directly sensitive, it reveals usage patterns and could be considered PII-adjacent.

**Recommended Fix:** Use 0600 for consistency.

---

## Positive Security Observations

The following areas are well-implemented and deserve recognition:

1. **A2A Constant-Time Comparison** (`internal/a2a/server.go:234`): Uses `subtle.ConstantTimeCompare` for all API key checks — resistant to timing attacks.

2. **API Key Migration System** (`internal/config/api_keys.go`): Automatically migrates plaintext API keys to `${ENV_VAR}` references with 0600 `keys.env` storage. Includes detection (`DetectPlaintextAPIKeys`) and forced `os.Chmod` after writes.

3. **Token Cache Isolation** (`internal/auth/a2a_token_cache.go`): Per-`{provider}-{clientID}` cache file naming prevents cross-instance token overwrites. Files use 0600 permissions.

4. **Crypto/rand Usage**: Nearly all security-sensitive random generation uses `crypto/rand` (session IDs, tunnel tokens, OAuth states, PKCE verifiers, webui auth tokens).

5. **Config API Sanitization** (`internal/webui/server.go:335`): The `sanitizeConfigForAPI` function properly strips sensitive fields before serving to the WebUI frontend.

6. **No InsecureSkipVerify**: Zero instances of `InsecureSkipVerify: true` in the codebase — all TLS verification is left at safe defaults.

7. **A2A Localhost Default**: When no auth is configured, the A2A server defaults to `127.0.0.1` and only accepts localhost connections, preventing accidental LAN exposure.

8. **Path Sandbox** (`internal/permission/sandbox.go`): Properly uses `filepath.Clean` + prefix-based checks for path restriction. Handles symlinks by resolving through `filepath.EvalSymlinks`.

9. **MCP OAuth Security**: Token exchange logs only boolean indicators (`has_access_token=%v`), not actual token values.

10. **Session JSONL Append**: Uses `0600` permissions for session append operations.

---

## Summary of Recommendations by Priority

| Priority | Finding | Effort |
|----------|---------|--------|
| P0 | C-1: Add authentication to ggcode-relay | Medium |
| P0 | C-2: Fix WebSocket CheckOrigin in webui | Small |
| P1 | H-1: Use constant-time comparison in webui auth | Small |
| P1 | H-2: Redact sensitive data from DingTalk error logs | Small |
| P1 | H-3: Change config file permissions to 0600 | Small |
| P1 | H-4: Set explicit 0600 on session temp files | Small |
| P2 | M-1: Add TLS support to relay server | Medium |
| P2 | M-2: Move tunnel token from URL to protocol message | Medium |
| P2 | M-3: Redact client_id from MCP OAuth logs | Small |
| P2 | M-5: Move WebUI auth from URL to cookie | Medium |
| P2 | M-6: Remove appKey from DingTalk log messages | Small |
| P3 | L-2: Add rate limiting to auth endpoints | Medium |
| P3 | L-4: Set WebSocket read limit | Small |
| P3 | L-5: Tighten cost file permissions | Small |

---

## Audit Methodology

This audit was conducted via static analysis of the Go source code, examining:

- All files under `internal/auth/`, `internal/a2a/`, `internal/permission/`, `internal/tunnel/`, `ggcode-relay/`, `internal/config/`, `internal/webui/`, `internal/im/`, `internal/mcp/`, `internal/session/`
- Credential handling patterns across `internal/config/`, `internal/provider/`, `internal/mcp/`
- File permission patterns across all packages
- Cryptographic primitive usage (random number generation, key comparison, hashing)
- WebSocket security patterns (CheckOrigin, authentication)
- All `debug.Log` and `fmt.Errorf` calls involving tokens, keys, secrets, or passwords
- Command execution patterns in `internal/tool/` and `internal/util/`
