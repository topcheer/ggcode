# ggcode Full Application Security Review

**Reviewer**: Senior Application Security Engineer  
**Date**: 2025-07-14  
**Scope**: Authentication, Authorization, Secrets Management, Input Validation, Cryptography, Network Security, Dependencies, Data Privacy

---

## Executive Summary

This review covers the complete ggcode application security posture across 7 domains. The application demonstrates several strong security practices -- PKCE-based OAuth2, JWT validation with JWKS rotation, AES-GCM encryption for tunnel data, config sanitization in the API, and 0600 file permissions for session/token stores.

However, 21 findings were identified, including 2 Critical, 4 High, 9 Medium, and 6 Low severity issues. The most urgent are: (1) relay server has no authentication, allowing any network-local attacker to hijack tunnels; (2) WebUI CheckOrigin allows all origins, enabling CSRF via WebSocket; (3) relay tokens transmitted in URL query strings leak via logs/referrers; (4) IM adapter `Extra` map secrets may leak through the config API.

---

## Findings

### SEC-001: Relay Server Has No Authentication
- **CWE**: CWE-306 (Missing Authentication for Critical Function)
- **Severity**: Critical
- **Location**: `ggcode-relay/main.go:580-641`
- **Description**: The relay server accepts WebSocket connections with only a "token" (any 16+ char string) as the room identifier. There is no authentication -- anyone who can reach the relay can create rooms or join existing rooms by guessing or brute-forcing tokens. The token is the sole authorization mechanism and serves as both room ID and shared secret.
- **Attack Scenario**: An attacker on the same network as the relay sends a WebSocket connection with `?role=server&token=<guessed_token>`. If the token matches an active room, they can intercept encrypted tunnel events, inject messages, or displace the legitimate server. For inactive rooms with persisted history, they can replay all stored events.
- **Recommendation**: 
  1. Add HMAC-based authentication: sign each WebSocket message with a server-side secret.
  2. Rate-limit connection attempts per IP.
  3. Use cryptographically random tokens (128-bit minimum) with a proper issuance protocol.
  4. Consider mutual TLS for relay connections.

### SEC-002: WebSocket CheckOrigin Always Returns True (CSRF)
- **CWE**: CWE-346 (Origin Validation Error) / CWE-352 (CSRF)
- **Severity**: Critical
- **Location**: `internal/webui/server_websocket.go:17`, `ggcode-relay/main.go:580`
- **Description**: Both the WebUI and relay WebSocket upgraders use `CheckOrigin: func(r *http.Request) bool { return true }`, which allows any origin to establish WebSocket connections. The WebUI uses a Bearer token in the URL hash, but the token is extracted client-side and attached as a query parameter for WebSocket connections.
- **Attack Scenario**: A malicious website opens a WebSocket connection to `http://localhost:<webui-port>/ws?token=<guessed_token>`. If the token can be obtained (e.g., via browser history, Referer header, or brute force), the attacker can send/receive messages as the authenticated user, execute arbitrary tool calls, and exfiltrate data.
- **Recommendation**:
  1. Validate the `Origin` header against `Host` in the WebUI upgrader.
  2. For the relay, validate against an allowed-origins list or use a pre-shared secret in headers.
  3. Use Sec-WebSocket-Protocol for token transport instead of URL query parameters.

### SEC-003: Relay Token Transmitted in URL Query String
- **CWE**: CWE-598 (Use of GET Request Method with Sensitive Query Strings)
- **Severity**: High
- **Location**: `internal/tunnel/relay_client.go:78`, `internal/tunnel/relay_client.go:474`
- **Description**: The relay token is passed as a URL query parameter: `fmt.Sprintf("%s/ws?role=server&token=%s", rc.relayURL, rc.token)`. This exposes the token in HTTP request logs, proxy logs, browser history, and Referer headers.
- **Attack Scenario**: A network monitoring tool or proxy captures the full URL including the token. The attacker uses the token to connect to the relay and impersonate the desktop or mobile client.
- **Recommendation**: Pass the token in a WebSocket subprotocol header or as an HTTP header during the upgrade handshake, not in the query string.

### SEC-004: Relay Server Serves Plain HTTP (No TLS)
- **CWE**: CWE-319 (Cleartext Transmission of Sensitive Information)
- **Severity**: High
- **Location**: `ggcode-relay/main.go:679`
- **Description**: The relay server uses `http.ListenAndServe(":"+port, mux)` with no TLS configuration. All communication, including encrypted tunnel payloads and metadata (session IDs, event IDs), is transmitted in cleartext. While the payload is AES-GCM encrypted, the metadata is visible.
- **Attack Scenario**: A network observer captures relay traffic, revealing session IDs, event IDs, and message timing. Even though payloads are encrypted, metadata analysis can reveal user activity patterns.
- **Recommendation**: Add TLS support with configurable certificate paths. If the relay must serve plain HTTP (e.g., behind a reverse proxy), document the requirement and add an option for direct TLS.

### SEC-005: IM Adapter `Extra` Map Secrets May Leak Through Config API
- **CWE**: CWE-200 (Exposure of Sensitive Information)
- **Severity**: High
- **Location**: `internal/webui/server.go:346` (`sanitizeConfigForAPI` includes `cfg.IM`), `internal/webui/server_handlers.go:552` (handleIMAdapterDetail returns full adapter config)
- **Description**: The `sanitizeMap` function only masks keys named exactly `"api_key"`, `"api_secret"`, or `"oauth_client_secret"`. IM adapter secrets stored in the `Extra` map (e.g., `bot_token`, `appsecret`, `client_secret`) pass through unmasked. The `/api/im/adapters/{name}` endpoint returns the full adapter config including the `Extra` map without sanitization.
- **Attack Scenario**: An attacker with WebUI access calls `GET /api/im/adapters/qq-bot-1` and reads the `extra.bot_token` and `extra.appsecret` fields, gaining access to the QQ bot's credentials.
- **Recommendation**: 
  1. Apply `sanitizeMap` recursively to the IM adapter Extra map.
  2. Add `bot_token`, `appsecret`, `app_id`, `client_secret`, `access_token`, and other known IM secret field names to the sanitize list.
  3. Better: use `looksLikeSecretField()` from `config/api_keys.go` for the sanitization check.

### SEC-006: readJSON Has No Body Size Limit
- **CWE**: CWE-400 (Uncontrolled Resource Consumption)
- **Severity**: Medium
- **Location**: `internal/webui/server.go:328-331`
- **Description**: The `readJSON` function uses `json.NewDecoder(r.Body).Decode(v)` without limiting the request body size. An attacker can send an extremely large JSON payload to any API endpoint, consuming server memory.
- **Attack Scenario**: An authenticated attacker sends a multi-gigabyte JSON body to `/api/config/scope`, causing OOM and crashing the application.
- **Recommendation**: Use `http.MaxBytesReader` to limit request body size (e.g., 1MB) before decoding.

### SEC-007: Tunnel Crypto Uses Static Salt for Key Derivation
- **CWE**: CWE-798 (Use of Hard-coded Credentials) / CWE-916 (Use of Password Hash With Insufficient Computational Effort)
- **Severity**: Medium
- **Location**: `internal/tunnel/crypto.go:29-31`
- **Description**: When the token is shorter than 16 bytes, `NewCrypto` derives an AES key using `argon2.IDKey(key, salt[:], 1, 64*1024, 4, 32)` with a static all-zero salt. This weakens the key derivation -- the same short token always produces the same derived key.
- **Attack Scenario**: If two users have the same short token, they get the same encryption key, allowing one to decrypt the other's tunnel traffic.
- **Recommendation**: Use a random salt that is stored alongside the encrypted data, or enforce minimum token length.

### SEC-008: Flutter Crypto Pads Short Keys with Zeros
- **CWE**: CWE-328 (Use of Weak Key)
- **Severity**: Medium
- **Location**: `mobile/flutter/lib/core/crypto.dart:14-22`
- **Description**: The `_normalizeKey` function pads keys shorter than 32 bytes with zero bytes. This means short tokens produce keys with significant zero-padding, reducing the effective key space.
- **Attack Scenario**: An attacker who knows the token is short (e.g., 8 bytes) can brute-force only 8 bytes of key material, knowing the remaining 24 bytes are zeros.
- **Recommendation**: Use proper key derivation (PBKDF2/argon2) for short tokens, consistent with the Go implementation.

### SEC-009: WebUI Bearer Token in URL Hash Fragment
- **CWE**: CWE-598 (Use of GET Request Method with Sensitive Query Strings)
- **Severity**: Medium
- **Location**: `internal/webui/server_static.go:14-35`
- **Description**: The SPA authentication scheme extracts a token from `location.hash` (`#token=<hex>`), then attaches it to all fetch and WebSocket requests. While hash fragments aren't sent to the server in normal HTTP, the injected JavaScript appends it as a WebSocket query parameter (`?token=...`).
- **Attack Scenario**: Browser extensions, XSS in the SPA, or browser history leaks could expose the token.
- **Recommendation**: Use cookie-based authentication with `SameSite=Strict` and `HttpOnly` flags, or use the `Sec-WebSocket-Protocol` header for WebSocket authentication.

### SEC-010: Relay Persists Encrypted Events to SQLite Without Cleanup Verification
- **CWE**: CWE-459 (Incomplete Cleanup)
- **Severity**: Medium
- **Location**: `ggcode-relay/store.go:262-314`
- **Description**: The relay stores encrypted events in SQLite with a 72-hour retention. However, the cleanup only runs every 6 hours and on startup. Between cleanup runs, stale data accumulates. Additionally, `destroyRoom` only deletes when explicitly called -- if the client disconnects without calling it, data persists until cleanup.
- **Attack Scenario**: If the relay is compromised, up to 72 hours of encrypted tunnel events are recoverable from the SQLite database. If the encryption key is also compromised, the full conversation history is exposed.
- **Recommendation**: 
  1. Reduce retention to the minimum viable (e.g., 24 hours).
  2. Run cleanup more frequently (e.g., every hour).
  3. Consider zeroing raw bytes before deleting rows.

### SEC-011: Claude OAuth Client ID Hardcoded
- **CWE**: CWE-798 (Use of Hard-coded Credentials)
- **Severity**: Low
- **Location**: `internal/auth/claude_oauth.go:21`
- **Description**: The Claude OAuth client ID (`9d1c250a-e61b-44d9-88ed-5944d1962f5e`) is hardcoded. This is a public OAuth client ID (not a secret), but it cannot be rotated without releasing a new version.
- **Attack Scenario**: If the OAuth app is revoked or needs rotation, all installed clients stop working until upgraded.
- **Recommendation**: Move to a configurable field with the current value as default. (Note: The Copilot client ID has this pattern with env var override.)

### SEC-012: GitHub Copilot Default Client ID Hardcoded
- **CWE**: CWE-798 (Use of Hard-coded Credentials)
- **Severity**: Low
- **Location**: `internal/auth/copilot.go:16`
- **Description**: The Copilot OAuth client ID (`Ov23li61W929PYwUl7RD`) is hardcoded but supports env var override via `GGCODE_GITHUB_COPILOT_CLIENT_ID`. This is acceptable for a public client ID.
- **Recommendation**: No action needed; the override mechanism exists.

### SEC-013: No CSRF Protection on WebUI REST API Endpoints
- **CWE**: CWE-352 (Cross-Site Request Forgery)
- **Severity**: High
- **Location**: `internal/webui/server_handlers.go` (all PUT/POST/DELETE handlers)
- **Description**: The WebUI REST API uses Bearer token authentication but has no CSRF tokens or `SameSite` cookie protection. An attacker's website can make authenticated requests if the token is known or stored in the browser.
- **Attack Scenario**: A malicious page makes `fetch('http://localhost:<port>/api/vendors/myvendor', {method: 'PUT', body: '{"api_key":"attacker-key"}'})` with the token obtained via SEC-009 or SEC-002, replacing the user's API key with an attacker-controlled one.
- **Recommendation**:
  1. Add CSRF tokens to state-changing operations.
  2. Validate `Origin` header on all POST/PUT/DELETE requests.
  3. Consider custom header requirement (e.g., `X-Requested-With`) that browsers add for fetch but not for form submissions.

### SEC-014: A2A Server Uses Subtle.ConstantTimeCompare for API Key but Logs Request Details
- **CWE**: CWE-532 (Insertion of Sensitive Information into Log File)
- **Severity**: Medium
- **Location**: `internal/a2a/server.go`
- **Description**: The A2A server correctly uses `subtle.ConstantTimeCompare` for API key validation (good), but some error paths log details about authentication failures that could help attackers enumerate valid configurations.
- **Attack Scenario**: An attacker observes log output (e.g., via a shared log aggregation system) to determine which auth methods are configured and which are not.
- **Recommendation**: Ensure authentication failure logs are generic (e.g., "authentication failed") without revealing which auth method was attempted or why it failed.

### SEC-015: A2A mDNS Discovery Broadcasts Service Without Authentication
- **CWE**: CWE-284 (Improper Access Control)
- **Severity**: Medium
- **Location**: `internal/a2a/mdns.go`
- **Description**: mDNS LAN discovery broadcasts the A2A service on the local network. While it requires auth to be configured, the broadcast itself reveals the service's existence and port to all LAN participants.
- **Attack Scenario**: An attacker on the same LAN detects the A2A service via mDNS and begins brute-forcing authentication.
- **Recommendation**: 
  1. Rate-limit A2A authentication attempts.
  2. Add an IP allowlist for A2A connections.
  3. Consider disabling mDNS by default and requiring explicit opt-in.

### SEC-016: Config File Writes May Race (TOCTOU)
- **CWE**: CWE-367 (Time-of-check Time-of-use Race Condition)
- **Severity**: Medium
- **Location**: `internal/config/config_save.go`
- **Description**: Config saves use atomic file write (write to temp + rename), but the read-modify-write cycle is not atomic. The WebUI holds a mutex, but the TUI and other components may modify the config file on disk outside the mutex.
- **Attack Scenario**: Two concurrent config modifications result in one being silently lost.
- **Recommendation**: Use file locking (e.g., `flock`) for config file access across processes.

### SEC-017: Session Data Stored in Plaintext JSONL
- **CWE**: CWE-311 (Missing Encryption of Sensitive Data)
- **Severity**: Medium
- **Location**: `internal/session/store.go`
- **Description**: Session data including full conversation history, tool call results, and tunnel events is stored as plaintext JSONL files in `~/.ggcode/sessions/`. While file permissions are 0600, the data is not encrypted at rest.
- **Attack Scenario**: An attacker with read access to the user's home directory (e.g., via a malicious backup tool, another compromised application, or a misconfigured file share) can read all conversation history.
- **Recommendation**: 
  1. Encrypt session files at rest using a user-derived key.
  2. At minimum, document the risk and recommend full-disk encryption.

### SEC-018: OAuth Token Cache Files Store Tokens in Plaintext JSON
- **CWE**: CWE-311 (Missing Encryption of Sensitive Data)
- **Severity**: Medium
- **Location**: `internal/auth/a2a_token_cache.go`, `internal/auth/store.go`
- **Description**: OAuth tokens are cached as plaintext JSON files in `~/.ggcode/oauth-tokens/` and `~/.ggcode/auth/`. File permissions are 0600, but the files contain access tokens and refresh tokens in cleartext.
- **Attack Scenario**: An attacker with file read access obtains OAuth tokens and uses them to impersonate the user.
- **Recommendation**: Encrypt token cache files with a key derived from the system keychain or a user passphrase.

### SEC-019: WebUI Port Binds to 127.0.0.1 Only (Good Practice)
- **CWE**: N/A (Positive Finding)
- **Severity**: Informational
- **Location**: `internal/webui/server.go`
- **Description**: The WebUI binds to `127.0.0.1:0` (random port on localhost), limiting exposure to the local machine. This is correct for a development tool.

### SEC-020: A2A Default Host Selection is Security-Aware (Good Practice)
- **CWE**: N/A (Positive Finding)
- **Severity**: Informational
- **Location**: `internal/a2a/server.go`
- **Description**: The A2A server defaults to `127.0.0.1` when no auth is configured, and `0.0.0.0` only when auth is explicitly set. This prevents accidental LAN exposure.

### SEC-021: JWT Validation Supports Multiple Algorithms Properly
- **CWE**: N/A (Positive Finding)
- **Severity**: Informational
- **Location**: `internal/auth/a2a_oauth.go`
- **Description**: JWT validation properly supports HS256, RS256, ECDSA with JWKS key rotation and configurable clock skew. Algorithm confusion attacks are mitigated by explicit algorithm checking.

### SEC-022: PKCE Implementation is Correct
- **CWE**: N/A (Positive Finding)
- **Severity**: Informational
- **Location**: `internal/auth/claude_oauth.go:67-89`, `internal/auth/pkce.go`
- **Description**: PKCE uses S256 (SHA-256) code challenge method, 32-byte random verifiers, and the state parameter is properly validated in the OAuth callback.

### SEC-023: No Rate Limiting on A2A Task Submission
- **CWE**: CWE-770 (Allocation of Resources Without Limits)
- **Severity**: Low
- **Location**: `internal/a2a/server.go:818`
- **Description**: The A2A server limits concurrent tasks to 5 (`maxConcurrentTasks`), but there is no rate limit on task submission. An authenticated attacker can flood the task queue.
- **Attack Scenario**: An authenticated A2A client submits thousands of tasks per second, exhausting server resources and blocking legitimate tasks.
- **Recommendation**: Add per-client rate limiting (e.g., 10 tasks/minute per authenticated identity).

### SEC-024: Relay Database File Permissions are 0755 for Directory
- **CWE**: CWE-732 (Incorrect Permission Assignment for Critical Resource)
- **Severity**: Low
- **Location**: `ggcode-relay/store.go:37`
- **Description**: The relay database directory is created with `os.MkdirAll(filepath.Dir(dbPath), 0o755)`. The directory should be 0700 to prevent other users from listing the database file.
- **Recommendation**: Change to `0o700` for the database directory.

### SEC-025: MCP OAuth Token Exchange Logs client_id and redirect_uri
- **CWE**: CWE-532 (Insertion of Sensitive Information into Log File)
- **Severity**: Low
- **Location**: `internal/mcp/oauth.go:595`
- **Description**: Debug logging includes `client_id` and `redirect_uri` during MCP OAuth token exchange. While not the secret itself, this information aids reconnaissance.
- **Recommendation**: Move client_id logging behind a verbose debug flag or remove it.

### SEC-026: Dependency Security Assessment
- **Severity**: Informational
- **Location**: `go.mod`
- **Description**: Key dependencies reviewed:
  - `golang-jwt/jwt/v5 v5.3.1` -- Well-maintained, no known vulnerabilities.
  - `gorilla/websocket v1.5.3` -- Well-maintained.
  - `golang.org/x/crypto v0.50.0` -- Current, no known vulnerabilities.
  - `modernc.org/sqlite v1.50.0` -- Pure Go SQLite, no CGO concerns.
  - `maunium.net/go/mautrix v0.27.0` -- Matrix library for WhatsApp bridge; ensure kept up to date.
  - `github.com/nbd-wtf/go-nostr v0.52.3` -- Nostr protocol library; verify upstream security.
- **Recommendation**: Enable `govulncheck` in CI pipeline for automated vulnerability scanning of dependencies.

---

## Summary Table

| ID | Severity | CWE | Component | Finding |
|----|----------|-----|-----------|---------|
| SEC-001 | Critical | CWE-306 | Relay | No authentication on relay server |
| SEC-002 | Critical | CWE-346 | WebUI + Relay | WebSocket CheckOrigin always true (CSRF) |
| SEC-003 | High | CWE-598 | Relay Client | Token in URL query string |
| SEC-004 | High | CWE-319 | Relay | No TLS, plaintext HTTP |
| SEC-005 | High | CWE-200 | WebUI | IM secrets leak through config API |
| SEC-006 | Medium | CWE-400 | WebUI | No request body size limit |
| SEC-007 | Medium | CWE-798/916 | Tunnel Crypto | Static salt for key derivation |
| SEC-008 | Medium | CWE-328 | Flutter Crypto | Zero-padded short keys |
| SEC-009 | Medium | CWE-598 | WebUI SPA | Token in URL hash fragment |
| SEC-010 | Medium | CWE-459 | Relay Store | Retained encrypted events up to 72h |
| SEC-011 | Low | CWE-798 | Auth | Hardcoded Claude OAuth client ID |
| SEC-012 | Low | CWE-798 | Auth | Hardcoded Copilot client ID (has override) |
| SEC-013 | High | CWE-352 | WebUI | No CSRF protection on REST API |
| SEC-014 | Medium | CWE-532 | A2A Server | Auth failure log information leakage |
| SEC-015 | Medium | CWE-284 | A2A mDNS | Service broadcast without auth |
| SEC-016 | Medium | CWE-367 | Config Save | TOCTOU race on config file writes |
| SEC-017 | Medium | CWE-311 | Sessions | Plaintext JSONL session storage |
| SEC-018 | Medium | CWE-311 | Auth Cache | Plaintext OAuth token cache |
| SEC-023 | Low | CWE-770 | A2A Server | No task submission rate limit |
| SEC-024 | Low | CWE-732 | Relay Store | DB directory permissions too open |
| SEC-025 | Low | CWE-532 | MCP OAuth | Logs client_id/redirect_uri |

---

## Positive Security Practices Observed

1. **PKCE OAuth2**: Proper S256 implementation with 32-byte random verifiers and state validation.
2. **JWT with JWKS**: Multi-algorithm JWT validation with key rotation support.
3. **AES-GCM encryption**: Tunnel payloads encrypted with proper AEAD.
4. **Config sanitization**: API responses mask API keys and remove env/headers.
5. **File permissions**: Session and token cache files use 0600.
6. **Atomic file writes**: Config and session saves use temp file + rename.
7. **Subtle constant-time comparison**: A2A API key validation uses `crypto/subtle.ConstantTimeCompare`.
8. **Localhost binding**: WebUI defaults to 127.0.0.1.
9. **Security-aware host selection**: A2A server only binds 0.0.0.0 when auth is configured.
10. **Read limits**: Relay WebSocket connections have 1MB read limits.
11. **Environment variable expansion**: API keys support `${ENV_VAR}` syntax, keeping secrets out of config files.
12. **Key migration**: `api_keys.go` provides migration from plaintext to env var references.

---

## Recommended Priority Actions

1. **Immediate** (Critical): Add authentication to the relay server (SEC-001).
2. **Immediate** (Critical): Fix WebSocket CheckOrigin in both WebUI and relay (SEC-002).
3. **Short-term** (High): Move relay token out of URL query string (SEC-003).
4. **Short-term** (High): Add TLS support to relay server (SEC-004).
5. **Short-term** (High): Sanitize IM adapter Extra map in config API (SEC-005).
6. **Short-term** (High): Add Origin header validation or CSRF tokens to WebUI REST API (SEC-013).
7. **Medium-term**: Add request body size limits (SEC-006).
8. **Medium-term**: Fix key derivation in tunnel crypto (SEC-007, SEC-008).
9. **Medium-term**: Implement at-rest encryption for sessions and token caches (SEC-017, SEC-018).
10. **Ongoing**: Add `govulncheck` to CI pipeline (SEC-026).

---

*This review was conducted via static analysis of the source code. Runtime testing, dynamic analysis, and penetration testing would provide additional coverage and are recommended as follow-up.*
