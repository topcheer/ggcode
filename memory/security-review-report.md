# Security Review Report — ggcode

**Reviewer:** security-reviewer  
**Date:** 2025-01-27  
**Scope:** Authentication/OAuth2, credential handling, command injection, permission model, input validation, token storage, WebUI, A2A networking  

---

## Executive Summary

The ggcode codebase demonstrates **strong security awareness** with multiple defense layers. The permission model, command gate, SSRF protection, and OAuth2/PKCE implementation are all well-designed. The codebase follows industry best practices for an AI coding agent: shell commands go through a multi-layer safety gate, file operations are sandboxed, and network fetch is protected against SSRF.

**Critical findings are minimal** — most issues are medium/low severity. The highest-risk areas are: (1) a JWT validation bypass path that falls back to lenient parsing, (2) the WebUI having no authentication and a permissive CORS/WebSocket origin check, and (3) a potential SSRF vector via A2A push notifications.

---

## Findings

### SEC-01: JWT Validation Falls Back to Lenient Parsing
**Severity: MEDIUM**  
**File:** `internal/auth/a2a_oauth.go` lines 438–453  
**Category:** Authentication Bypass

The `validateJWT` method first tries parsing with strict issuer/audience checks (`jwt.WithIssuer`, `jwt.WithAudience`). If that fails for any reason other than expiration, it **retries without those checks**:

```go
// Try parsing without strict issuer/audience validation
token, err = jwt.ParseWithClaims(tokenString, jwt.MapClaims{}, keyFunc)
```

This fallback means a token with a valid signature but **wrong issuer or audience** will still be accepted. An attacker who obtains a valid JWT from a different issuer (e.g., a different OAuth provider using the same signing key) could authenticate.

**Recommendation:** Remove the fallback. If issuer/audience validation fails, reject the token. If some providers use unexpected issuer URLs, address that at the configuration level.

---

### SEC-02: WebUI Has No Authentication
**Severity: MEDIUM**  
**File:** `internal/webui/server.go`  
**Category:** Missing Access Control

The WebUI server binds to `127.0.0.1:0` (random port) but has **no authentication layer** on any endpoint. Any local process can:
- Read full configuration (including masked API keys)
- Set/modify API keys via `PUT /api/vendors/{vendor}/endpoints/{endpoint}/apikey`
- Send chat messages as the user via WebSocket
- Restart the agent via `POST /api/restart`
- Modify IM adapter settings

While localhost-only binding is a reasonable mitigation, on shared/multi-user systems or in container environments with port forwarding, this is exploitable.

**Recommendation:** Add an optional authentication token (e.g., generated at startup, displayed once, or configured via env var). At minimum, document this as a known limitation for non-single-user environments.

---

### SEC-03: WebSocket Origin Check Allows All Origins
**Severity: MEDIUM**  
**File:** `internal/webui/server.go` line 1235–1237  
**Category:** Cross-Site WebSocket Hijacking (CSWSH)

```go
var upgrader = websocket.Upgrader{
    CheckOrigin: func(r *http.Request) bool { return true },
}
```

This allows **any** origin to establish a WebSocket connection. Combined with SEC-02 (no auth), a malicious webpage could connect to the WebSocket (if the port is known/guessable) and send commands as the user. Since the server is localhost-only and the port is random, exploitation requires specific conditions, but the risk increases if port forwarding or reverse proxying is used.

**Recommendation:** Restrict `CheckOrigin` to `localhost`/`127.0.0.1` origins, or validate against the actual server address.

---

### SEC-04: Auth Store Directory Has World-Readable Permissions
**Severity: LOW**  
**File:** `internal/auth/store.go` line 146  
**Category:** Credential Exposure

```go
if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
```

The parent directory `~/.ggcode/` is created with `0755` (world-readable/traversable). The file itself is correctly set to `0600`, but on multi-user systems, other users can discover the file exists and see its metadata.

**Recommendation:** Use `0700` for `~/.ggcode/` directory creation.

---

### SEC-05: A2A Push Notifications — SSRF via User-Controlled URL
**Severity: MEDIUM**  
**File:** `internal/a2a/server.go` lines 755–793  
**Category:** Server-Side Request Forgery (SSRF)

Push notification configurations are stored in-memory and the URL is user-provided via JSON-RPC. The `firePushNotifications` method makes HTTP POST requests to these URLs using `http.DefaultClient` (no timeout, no private network filtering):

```go
resp, err := http.DefaultClient.Do(req)
```

An authenticated A2A client can set a push URL pointing to internal services (e.g., `http://169.254.169.254/latest/meta-data/` for cloud metadata, or `http://localhost:PORT/internal-endpoint`).

**Recommendation:** Apply the same SSRF protection used in `web_fetch.go` to push notification URLs. Also add a reasonable timeout.

---

### SEC-06: sanitizeConfigForAPI Exposes Full Vendor/Endpoint/MCP Config
**Severity: LOW-MEDIUM**  
**File:** `internal/webui/server.go` line 1769–1788  
**Category:** Information Disclosure

```go
func sanitizeConfigForAPI(cfg *config.Config) map[string]interface{} {
    return map[string]interface{}{
        ...
        "vendors":        cfg.Vendors,
        "mcp_servers":    cfg.MCPServers,
        ...
    }
}
```

While API keys are masked in some responses (`has_api_key: true/false`), the full `vendors` and `mcp_servers` structs are returned in the config endpoint. Depending on the struct contents, this may expose MCP server `env` blocks (which could contain `${VAR}` references revealing env var names) and endpoint `base_url` values. The `handleVendorDetail` GET response does correctly mask the key (`has_api_key` bool), but the config dump is comprehensive.

**Recommendation:** Apply recursive masking to all string fields matching secret patterns in the config API response, or restrict the config dump to non-sensitive fields.

---

### SEC-07: Shell Command via LLM — No Shell Injection Sanitization on working_dir
**Severity: LOW**  
**File:** `internal/tool/run_command.go` line 238  
**Category:** Defense in Depth

The `run_command` tool correctly ignores LLM-provided `working_dir` (uses `t.WorkingDir` instead). This is a good security practice documented in the comment: "Use the fixed WorkingDir from agent, ignore LLM-provided working_dir." However, the command itself is passed directly to the shell via `util.NewShellCommandContext`, which invokes `bash -c`. While the command gate provides regex-based filtering, it doesn't apply formal shell escaping — complex edge cases in shell parsing could potentially bypass the regex rules.

**Recommendation:** This is already well-mitigated by the multi-layer command gate. Consider adding a test suite of adversarial command patterns (fuzzing) for ongoing validation.

---

### SEC-08: HMAC Key Uses client_id Instead of client_secret
**Severity: LOW**  
**File:** `internal/auth/a2a_oauth.go` lines 427–430  
**Category:** Cryptographic Misuse

```go
case "HS256", "HS384", "HS512":
    // HMAC — client_secret is the key (for opaque token emulation)
    if v.clientID == "" {
        return nil, fmt.Errorf("HMAC token but no client_id configured")
    }
    return []byte(v.clientID), nil
```

The HMAC key is set to `clientID` (public identifier) rather than `client_secret`. If a provider issues HS256 JWTs, anyone who knows the public `client_id` could forge tokens. This is a known anti-pattern (the "none/HS256" attack vector in JWT implementations).

**Recommendation:** Either require `client_secret` for HMAC validation, or reject HS256 tokens entirely in favor of asymmetric algorithms (RS256/ES256).

---

### SEC-09: Copilot Device Code Logged to Debug
**Severity: LOW**  
**File:** `internal/auth/copilot.go` (debug output)  
**Category:** Information Exposure

While no debug logs were found that directly print tokens or secrets in the auth package, the debug logging infrastructure (`debug.Log`) could potentially expose sensitive values if debug mode is enabled and logs are shared. The code does log the device code to clipboard which is by design, but ensure debug logs don't persist the user code.

**Recommendation:** Add a sanitization pass in `debug.Log` for any output that matches token-like patterns, or document that debug mode should not be used in production.

---

### SEC-10: `AllowPrivate` Flag in WebFetch Bypasses All SSRF Protection
**Severity: LOW (Design)**  
**File:** `internal/tool/web_fetch.go` line 29  
**Category:** Security Bypass

```go
type WebFetch struct {
    AllowPrivate bool  // AllowPrivate disables SSRF protection. Only use for testing.
}
```

The `AllowPrivate` field completely disables SSRF protection. If this is accidentally set to `true` in production code paths, all SSRF protections are bypassed. Currently, it appears to only be set in test code.

**Recommendation:** Consider making this unexported or requiring an explicit build tag to enable.

---

## Positive Security Findings

The following security measures are well-implemented:

1. **PKCE Implementation** — Cryptographically secure code verifier/challenge generation using `crypto/rand` with S256. Proper state parameter validation in OAuth callbacks.

2. **Command Gate (Defense in Depth)** — Three-layer model (Block/Ask/Allow) with comprehensive regex rules covering catastrophic commands, injection patterns, privilege escalation, and destructive operations. Pre-checks for control characters and Unicode whitespace bypass attempts.

3. **SSRF Protection** — `web_fetch` tool has thorough SSRF mitigation: hostname resolution blocking, private IP range filtering, redirect chain validation, and custom DialContext to prevent DNS rebinding.

4. **Path Sandbox** — Symlink-aware path sandboxing resolves symlinks to prevent escape. Separate read/write sandbox enforcement in bypass modes.

5. **Token Storage** — OAuth tokens cached with `0600` permissions. Per-client isolation prevents cross-instance token overwrites. Auth store uses atomic write (tmp + rename).

6. **A2A Authentication** — Multi-layer: API key with constant-time comparison (`subtle.ConstantTimeCompare`), Bearer token validation with JWKS, mTLS support, and secure localhost-only default when no auth is configured.

7. **Session Storage** — Session files written with `0600` permissions using append-only JSONL.

8. **API Key Detection** — `DetectPlaintextAPIKeys` scans config files for plaintext secrets and recommends env var substitution.

9. **Permission Model** — Five-mode escalation cycle with appropriate restrictions per mode. Plan mode is strictly read-only. Bypass/autopilot modes still enforce sandbox boundaries for file writes.

10. **Body Size Limits** — A2A server caps request body to 4 MiB (`http.MaxBytesReader`). WebFetch limits responses to 10 MiB.

---

## Summary Table

| ID | Severity | Category | Component | Finding |
|----|----------|----------|-----------|---------|
| SEC-01 | MEDIUM | Auth Bypass | auth/a2a_oauth | JWT validation fallback skips issuer/audience checks |
| SEC-02 | MEDIUM | Missing Auth | webui | WebUI has no authentication on any endpoint |
| SEC-03 | MEDIUM | CSWSH | webui | WebSocket allows all origins |
| SEC-04 | LOW | Credential Exposure | auth/store | Directory created with 0755 |
| SEC-05 | MEDIUM | SSRF | a2a/server | Push notification URLs not validated against private networks |
| SEC-06 | LOW-MEDIUM | Info Disclosure | webui | Config API exposes full vendor/MCP structures |
| SEC-07 | LOW | Defense in Depth | tool/run_command | Shell commands rely on regex gate without formal escaping |
| SEC-08 | LOW | Cryptographic | auth/a2a_oauth | HMAC uses clientID (public) as signing key |
| SEC-09 | LOW | Info Exposure | auth | Debug logs could expose sensitive values |
| SEC-10 | LOW | Design | tool/web_fetch | AllowPrivate flag bypasses all SSRF protection |

---

## Recommendations Priority

1. **High Priority:** Fix SEC-01 (JWT fallback), SEC-02 (WebUI auth), SEC-03 (WebSocket origin)
2. **Medium Priority:** Fix SEC-05 (push notification SSRF), SEC-06 (config API exposure), SEC-08 (HMAC key)
3. **Low Priority:** Fix SEC-04 (directory perms), SEC-07 (command fuzzing), SEC-09 (debug sanitization), SEC-10 (AllowPrivate guard)
