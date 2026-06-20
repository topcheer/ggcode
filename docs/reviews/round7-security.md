# Security Review Report - Round 7

**Date**: 2025-07-17  
**Reviewer**: Automated Security Audit  
**Scope**: Full codebase security review of ggcode  
**Previous Audit**: `security/SECURITY_AUDIT_REPORT.md` (2025-07-14)

---

## Executive Summary

This report provides an updated security review of the ggcode codebase. The previous audit (Round 6, `security/SECURITY_AUDIT_REPORT.md`) identified 2 Critical, 6 High, 8 Medium, and 6 Low findings. This round re-validates those findings and identifies additional issues.

**Status of prior findings**: All previously reported findings (C-01 through L-06) remain unfixed as of this review. See the "Prior Findings Status" section for details.

**New findings in this round**: 0 Critical, 1 High, 4 Medium, 3 Low.

---

## Prior Findings Status (Unfixed)

All findings from the previous audit remain valid and unfixed:

| Prior ID | Severity | Status | Component |
|----------|----------|--------|-----------|
| C-01 | Critical | **UNFIXED** | WebUI Auth - Timing attack on token comparison |
| C-02 | Critical | **UNFIXED** | IM/DingTalk - Access token logged in cleartext |
| H-01 | High | **UNFIXED** | Relay - WebSocket allows all origins |
| H-02 | High | **UNFIXED** | Relay - Token in URL query parameter |
| H-03 | High | **UNFIXED** | Relay - Token prefix logged |
| H-04 | High | **UNFIXED** | WebUI - No CORS configuration |
| H-05 | High | **UNFIXED** | A2A - Push notification SSRF |
| H-06 | High | **UNFIXED** | Config - Plaintext API key storage |
| M-01 | Medium | **UNFIXED** | Plugin - Argument splitting |
| M-02 | Medium | **UNFIXED** | Tunnel - Static salt in KDF |
| M-03 | Medium | **UNFIXED** | Tools - Nil sandbox bypass |
| M-04 | Medium | **UNFIXED** | Config - keys.env not in .gitignore |
| M-05 | Medium | **UNFIXED** | WebUI - Token in URL fragment |
| M-06 | Medium | **UNFIXED** | Auth - No rate limiting |
| M-07 | Medium | **UNFIXED** | Relay - No TLS |
| M-08 | Medium | **UNFIXED** | A2A - Agent card unauthenticated |

---

## New Findings

### HIGH

#### H-07: Relay Token Minimum Length Too Short (16 characters)
- **File**: `ggcode-relay/main.go:795`
- **Type**: CWE-326 (Inadequate Encryption Strength)
- **Description**: The relay server enforces a minimum token length of only 16 characters:
  ```go
  if len(token) < 16 {
      http.Error(w, "token too short", http.StatusBadRequest)
      return
  }
  ```
  A 16-character hex string provides only 64 bits of entropy. With no rate limiting and network accessibility (the relay is designed to be deployed publicly), this is brute-forceable. A 16-char hex token has 16^16 = 2^64 possible values, which is at the edge of practical brute-force attacks for a well-funded attacker with a fast network connection.
- **Risk**: An attacker who can reach the relay server could brute-force room tokens to access or inject messages into any tunnel session.
- **Mitigation**: Increase the minimum token length to at least 32 hex characters (128 bits). Consider implementing rate limiting on failed connection attempts to make brute-force infeasible at current length.

---

### MEDIUM

#### M-09: WebUI WebSocket No Message Size Limit
- **File**: `internal/webui/server_websocket.go:128-134`
- **Type**: CWE-400 (Uncontrolled Resource Consumption)
- **Description**: The WebSocket read loop in `handleChatWS` does not set a read limit:
  ```go
  for {
      _, msgBytes, err := conn.ReadMessage()
      if err != nil {
          // ...
      }
  }
  ```
  While the HTTP body for A2A is limited via `MaxBytesReader` (4 MiB), the WebSocket connections have no message size cap. An authenticated attacker could send extremely large messages to exhaust server memory. The gorilla/websocket default read limit is 0 (unlimited) unless `SetReadLimit` is called.
- **Risk**: Memory exhaustion DoS by an authenticated user sending massive WebSocket frames. Since the WebUI token is single-use per session and localhost-bound, practical exploitation requires local access.
- **Mitigation**: Call `conn.SetReadLimit(N)` (e.g., 1 MiB) after the WebSocket upgrade to cap individual message sizes.

#### M-10: Relay WebSocket No Message Size Limit
- **File**: `ggcode-relay/main.go:205-280`
- **Type**: CWE-400 (Uncontrolled Resource Consumption)
- **Description**: Similar to M-09, the relay server's `readPump` reads messages from WebSocket connections without size limits. The relay is publicly accessible, making this more severe than M-09.
  ```go
  func (p *peer) readPump(h *hub) {
      // ...
      for {
          _, message, err := p.conn.ReadMessage()
          // no size check
      }
  }
  ```
- **Risk**: Any client connected to the relay (even without a valid room token) could exhaust the relay server's memory by sending oversized WebSocket frames after upgrade.
- **Mitigation**: Set `conn.SetReadLimit()` to a reasonable cap (e.g., 1 MiB) after WebSocket upgrade.

#### M-11: WeCom Adapter No Webhook Signature Verification
- **File**: `internal/im/wecom_adapter.go:42-60`
- **Type**: CWE-345 (Insufficient Verification of Data Authenticity)
- **Description**: The WeCom adapter uses a WebSocket connection model rather than HTTP webhooks, which avoids the traditional webhook signature verification issue. However, the adapter's `secret` field (line 47) is stored in plaintext in the configuration and used for token computation. Unlike the Feishu adapter which properly verifies webhook signatures using HMAC-SHA256 with `hmac.Equal` (constant-time comparison), the WeCom adapter does not have an equivalent verification step because it uses a push (WebSocket) model rather than pull (webhook) model. This is architecturally sound for WebSocket, but the secret stored in config remains a risk.
- **Risk**: Low in practice due to WebSocket architecture. The `secret` field in config is at risk if config files are exposed.
- **Mitigation**: Ensure WeCom `secret` values are stored via `${ENV_VAR}` references rather than plaintext in YAML. Document this recommendation.

#### M-12: WebUI A2A API Key Written Back to Config Without Sanitization
- **File**: `internal/webui/server_handlers.go:719-726`
- **Type**: CWE-312 (Cleartext Storage of Sensitive Information)
- **Description**: The WebUI config API allows setting A2A authentication keys via the REST endpoint:
  ```go
  if req.Auth.APIKey != "" {
      s.cfg.A2A.Auth.APIKey = req.Auth.APIKey
  }
  if req.Auth.APIKeys != nil {
      s.cfg.A2A.Auth.APIKeys = req.Auth.APIKeys
  }
  ```
  These values are then persisted to the YAML config file via `s.saveConfig()`. The API key values are stored as plaintext in the YAML file, not as environment variable references. This differs from the automatic migration behavior for LLM provider API keys which are migrated to `keys.env`.
- **Risk**: A2A API keys stored in YAML config file in plaintext, visible to any process with file read access.
- **Mitigation**: Apply the same migration logic used for provider API keys (migrate to env var references + keys.env) to A2A auth keys. At minimum, document the risk.

---

### LOW

#### L-07: Relay Dockerfile Runs as Root
- **File**: `ggcode-relay/Dockerfile:8-11`
- **Type**: CWE-250 (Execution with Unnecessary Privileges)
- **Description**: The relay Docker image runs as root:
  ```dockerfile
  FROM alpine:3.19
  COPY --from=builder /relay /relay
  EXPOSE 8080
  CMD ["/relay"]
  ```
  No `USER` directive is present. If the relay process is compromised (e.g., via a vulnerability in WebSocket handling), the attacker has root access within the container.
- **Risk**: Container escape or privilege escalation if relay is compromised. Mitigated by container isolation but violates defense-in-depth.
- **Mitigation**: Add a non-root user:
  ```dockerfile
  RUN adduser -D -H relay
  USER relay
  CMD ["/relay"]
  ```

#### L-08: A2A Server mTLS Authentication Does Not Validate Certificate Identity
- **File**: `internal/a2a/server.go:254-258`
- **Type**: CWE-295 (Improper Certificate Validation)
- **Description**: The mTLS authentication check only verifies that a client certificate was presented:
  ```go
  if s.mtlsEnabled {
      if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
          return true
      }
      return false
  }
  ```
  It does not validate which certificate was presented (no SAN/CN checking). Any valid client certificate trusted by the configured CA will be accepted, even if it belongs to a different service or identity.
- **Risk**: In environments where the CA issues certificates to multiple services, any service with a valid certificate can authenticate to the A2A server. This is a common mTLS configuration mistake.
- **Mitigation**: Add certificate identity validation (verify SAN/CN against an allowlist of expected identities).

#### L-09: Anthropic Bootstrap Reads Claude Desktop Settings
- **File**: `internal/config/anthropic_bootstrap.go:81-103`
- **Type**: CWE-200 (Exposure of Sensitive Information)
- **Description**: The bootstrap process reads the user's Claude Desktop settings files (`~/.claude/settings.json`, `~/.claude.json`) to extract environment variables (including API keys):
  ```go
  func loadClaudeEnv() map[string]string {
      for _, path := range knownClaudeSettingsPaths() {
          data, err := os.ReadFile(path)
          // ...
          for key, value := range parsed.Env {
              out[key] = strings.TrimSpace(value)
          }
      }
      return out
  }
  ```
  While this is a user convenience feature, it extracts credentials from another application's configuration without explicit user consent. The Claude settings file may contain keys the user did not intend to use with ggcode.
- **Risk**: Low - this only runs on the local machine and only during first-launch bootstrap. However, it reads credentials from a third-party application's config without user confirmation.
- **Mitigation**: Display a one-time prompt asking the user if they want to import credentials from Claude Desktop settings before doing so.

---

## Positive Security Findings (Confirmed)

The following positive findings from the prior audit are re-confirmed:

1. **A2A API key comparison uses constant-time**: `crypto/subtle.ConstantTimeCompare` in `internal/a2a/server.go:234`
2. **Relay token stored as SHA-256 hash in SQLite**: `ggcode-relay/store.go:428-431`
3. **Feishu adapter uses HMAC-SHA256 with constant-time comparison**: `internal/im/feishu_adapter.go:597-601`
4. **Config API sanitizes sensitive values**: `internal/webui/server.go:333-391`
5. **AES-GCM used for tunnel encryption**: `internal/tunnel/crypto.go`
6. **MaxBytesReader caps A2A request body**: `internal/a2a/server.go:208`
7. **keys.env uses 0600 permissions**: `internal/config/api_keys.go:444-448`
8. **OAuth token cache files use 0600 permissions**: `internal/auth/store.go`
9. **Comprehensive command gate**: Blocks destructive commands in tool execution
10. **All relay SQLite queries use parameterized statements**: No SQL injection risk

---

## Summary Table (All Findings)

| ID | Severity | Component | Issue | Status |
|----|----------|-----------|-------|--------|
| C-01 | Critical | WebUI Auth | Timing attack on token comparison | UNFIXED |
| C-02 | Critical | IM/DingTalk | Access token logged in cleartext | UNFIXED |
| H-01 | High | Relay | WebSocket allows all origins | UNFIXED |
| H-02 | High | Relay | Token in URL query parameter | UNFIXED |
| H-03 | High | Relay | Token prefix logged | UNFIXED |
| H-04 | High | WebUI | No CORS configuration | UNFIXED |
| H-05 | High | A2A | Push notification SSRF | UNFIXED |
| H-06 | High | Config | Plaintext API key storage | UNFIXED |
| **H-07** | **High** | **Relay** | **Token minimum length too short (16 chars)** | **NEW** |
| M-01 | Medium | Plugin | Argument splitting inconsistency | UNFIXED |
| M-02 | Medium | Tunnel | Static salt in KDF | UNFIXED |
| M-03 | Medium | Tools | Nil sandbox bypass | UNFIXED |
| M-04 | Medium | Config | keys.env not in .gitignore | UNFIXED |
| M-05 | Medium | WebUI | Token in URL fragment | UNFIXED |
| M-06 | Medium | Auth | No rate limiting | UNFIXED |
| M-07 | Medium | Relay | No TLS | UNFIXED |
| M-08 | Medium | A2A | Agent card unauthenticated | UNFIXED |
| **M-09** | **Medium** | **WebUI** | **WebSocket no message size limit** | **NEW** |
| **M-10** | **Medium** | **Relay** | **WebSocket no message size limit** | **NEW** |
| **M-11** | **Medium** | **IM/WeCom** | **Secret stored in plaintext config** | **NEW** |
| **M-12** | **Medium** | **WebUI** | **A2A API key written back as plaintext** | **NEW** |
| L-01 | Low | WebUI | WebSocket token in query | UNFIXED |
| L-02 | Low | Docs | Placeholder secrets | UNFIXED |
| L-03 | Low | Tools | Shell execution (mitigated) | UNFIXED |
| L-04 | Low | Config | Token file permissions (good) | N/A |
| L-05 | Low | WebUI | Uses crypto/rand (good) | N/A |
| L-06 | Low | Tunnel | Uses Argon2id (good) | N/A |
| **L-07** | **Low** | **Relay/Docker** | **Dockerfile runs as root** | **NEW** |
| **L-08** | **Low** | **A2A** | **mTLS no certificate identity check** | **NEW** |
| **L-09** | **Low** | **Config** | **Bootstrap reads Claude Desktop settings** | **NEW** |

---

## Detailed Findings by Area

### 1. internal/auth/ (OAuth2 PKCE, Device Flow, OIDC, JWT, Token Cache)

**Overall Assessment**: The auth subsystem is well-designed with proper use of `crypto/rand` for token generation, PKCE for OAuth2 flows, and JWT validation with JWKS key rotation. Token cache files use 0600 permissions with per-client isolation.

**Findings**:
- Token cache path derivation (`a2a_token_cache.go:137`) truncates clientID to 12 characters, which is adequate for isolation.
- Copilot OAuth uses Device Flow correctly (`copilot.go:140-206`) with proper polling and backoff.
- PKCE code verifier generation not shown in source (wrapper only at `pkce.go:4`), but the function signatures indicate proper S256 challenge derivation.
- No timing attack concerns: A2A auth uses `subtle.ConstantTimeCompare`. The WebUI auth does NOT (see C-01).

### 2. internal/config/ (API Key Handling, Env Expansion, Secret Storage)

**Overall Assessment**: API keys are migrated from YAML plaintext to `keys.env` files with 0600 permissions. Env var expansion (`${VAR}` syntax) is implemented correctly.

**Findings**:
- `api_keys.go:444-448`: keys.env written with `0600` + explicit `chmod` (good).
- `env.go`: Environment expansion handles `${VAR}`, `${VAR:-default}`, `${VAR:+fallback}` correctly without injection risks.
- A2A API keys are NOT migrated to keys.env (see M-12).

### 3. internal/permission/ (Permission Modes, Tool Policy, Sandbox, Dangerous Tools)

**Overall Assessment**: Five permission modes provide appropriate defense-in-depth. The dangerous tool classification (`dangerous.go`) covers shell injection, pipe redirects, and destructive commands.

**Findings**:
- Sandbox bypass when policy is nil (M-03, previously reported).
- `config_policy.go` correctly evaluates per-tool rules with mode-specific defaults.
- Dangerous tool classification uses regex patterns that cover common attack vectors.

### 4. internal/tunnel/ + internal/webui/ (WebSocket Security)

**Overall Assessment**: The tunnel uses AES-GCM encryption for data in transit. The WebUI binds to localhost only. Both use gorilla/websocket with `CheckOrigin: return true`.

**Findings**:
- WebUI WebSocket `CheckOrigin: return true` (`server_websocket.go:17`) - mitigated by localhost-only binding + auth token.
- Relay WebSocket `CheckOrigin: return true` (`main.go:786`) - publicly accessible (see H-01).
- No WebSocket message size limits (see M-09, M-10).
- WebUI auth script injection into SPA HTML (`server_static.go:13-35`) is well-implemented: extracts token from URL hash, uses `history.replaceState` to clear it, and attaches to all subsequent requests.

### 5. ggcode-relay/ (Relay Server)

**Overall Assessment**: The relay server is a standalone WebSocket relay with SQLite persistence. Token hashes (SHA-256) are stored in the database rather than plaintext tokens. SQL queries are all parameterized.

**Findings**:
- No authentication on the relay itself (token is the room identifier, not auth).
- CheckOrigin returns true for all origins (H-01).
- Token passed in URL query parameter (H-02).
- No TLS support (M-07).
- Docker image runs as root (L-07).
- Token minimum length of 16 chars is too short (H-07).

### 6. internal/a2a/ (A2A Multi-Auth)

**Overall Assessment**: The A2A server implements multiple authentication methods with proper security:
- API key: constant-time comparison
- OAuth2/OIDC: Bearer token validation with JWT/JWKS
- mTLS: Client certificate verification at TLS level
- No-auth fallback: localhost-only (via `isLocalRequestHost`)

**Findings**:
- Push notification SSRF (H-05, previously reported).
- mTLS identity validation gap (L-08, new).
- Agent card endpoints are unauthenticated (by A2A spec, M-08).

### 7. internal/im/ (IM Adapters)

**Overall Assessment**: IM adapters have per-channel access control (allowed users/channels). The Feishu adapter has the most robust security with HMAC-SHA256 webhook signature verification, timestamp freshness checks, and nonce replay protection.

**Findings**:
- DingTalk adapter logs access token in cleartext on error (C-02, previously reported).
- DingTalk logs appKey on successful token refresh (C-02, previously reported).
- WeCom secret stored in config as plaintext (M-11, new).
- Telegram, Discord, Slack, QQ adapters use platform-provided authentication (bot tokens, webhook secrets) correctly.

### 8. Secret Scanning

**Overall Assessment**: No hardcoded credentials, API keys, or tokens were found in the source code. Example configuration uses obvious placeholder values.

**Findings**:
- No `.env` files found in the repository (good).
- `.gitignore` covers `ggcode.yaml` and `.ggcode/` but not `keys.env` explicitly (M-04).
- `ggcode-relay/relay.db` (SQLite database) is present in the repo but is gitignored per `ggcode-relay/.gitignore`.
- No API keys, tokens, or credentials found in Go source files.

---

## Recommendations (Priority Order)

### Immediate (Critical/High)

1. **Fix C-01**: Replace `==` with `subtle.ConstantTimeCompare` in `internal/webui/auth.go:35,42`. This is a one-line fix.
2. **Fix C-02**: Remove accessToken and appKey from `debug.Log` output in `internal/im/dingtalk_adapter.go:512,526`.
3. **Fix H-07**: Increase relay token minimum length from 16 to 32 characters.
4. **Fix H-05**: Add URL validation for A2A push notification targets (block private IPs, require HTTPS).

### Short-term (High)

5. **Fix H-01**: Implement origin validation in relay WebSocket upgrader.
6. **Fix H-03**: Use token hash in relay logs instead of prefix.
7. **Fix H-04**: Add explicit CORS headers to WebUI server.
8. **Fix H-06**: Document keys.env storage model; consider OS keychain.

### Medium-term (Medium)

9. **Fix M-09/M-10**: Add `SetReadLimit()` to WebSocket connections in both WebUI and relay.
10. **Fix M-06**: Implement basic rate limiting on authentication endpoints.
11. **Fix M-07**: Add TLS support to relay server or document reverse proxy requirement.
12. **Fix M-12**: Apply API key migration to A2A auth keys.
13. **Fix M-04**: Add `keys.env` to `.gitignore`.

### Low Priority

14. **Fix L-07**: Add non-root user to relay Dockerfile.
15. **Fix L-08**: Add certificate identity validation to A2A mTLS auth.
16. **Fix M-03**: Default sandbox to working directory when no policy is set.
17. **Fix M-11**: Document recommendation to use env var references for IM secrets.
