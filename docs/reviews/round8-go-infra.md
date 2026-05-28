# Round 8 Go Infrastructure Security Review

**Reviewer**: go-infra (automated)
**Date**: 2025-07-27
**Scope**: `internal/{config,auth,mcp,a2a,harness,cron,tunnel,im,webui,daemon,commands,memory,lsp}`
**Coverage**: 161 non-test Go source files (~1.7M bytes), all files read
**Commit**: HEAD

---

## Summary

Full review of 13 infrastructure packages. The codebase demonstrates generally strong security practices: constant-time API key comparison, atomic file writes with rename, encrypted tunnel transport (AES-GCM), per-client-isolated token caches, correct JSON-RPC 2.0 protocol, and proper mutex usage across most concurrent access patterns.

**Findings**: 2 Critical, 4 High, 14 Medium, 10 Low = **30 total**.

---

## Findings by Package

### 1. internal/config/ -- Config Loading, Env Expansion, API Keys

Files reviewed: `config.go` (1329 lines), `api_keys.go` (664), `env.go` (263), `config_save.go` (502), `config_keys.go` (132), `config_vendor.go` (355), `anthropic_bootstrap.go` (288), `vendor_defaults.go` (987), `context_window.go` (786), `instance_delta.go` (376), `a2a_override.go` (66), `knight.go` (150), `onboard.go` (117), `instance.go` (639).

#### [C-1] CRITICAL -- Expanded Env Vars May Leak Through WebUI REST API

**File**: `config.go` lines 900+, `env.go` lines 30-100
**Functions**: `ExpandEnv`, vendor config loading, WebUI `GET /api/config/vendors`

Env var expansion uses `os.ExpandEnv` on all string config values (API keys, endpoints, secrets). The expanded values live in the `Config` struct. The WebUI `GET /api/vendors/{vendor}` endpoint (`server_handlers.go` lines ~600-650) serializes vendor config including `ExtraHeaders` (which may contain `${ENV_VAR}` expanded secrets) directly to JSON.

The `api_keys.go` `MaskKey()` and `SanitizeConfigForDisplay()` functions mask keys in log output, but the WebUI REST handlers access the live `Config` struct (with expanded values), not the sanitized copy. An attacker with network access to the WebUI (localhost) can read expanded API keys and secrets.

**Recommendation**: Create a sanitized copy of config for all REST endpoints. Never return `ExtraHeaders`, `api_key`, or any field that contained `${...}` in its raw form. Redact at the serialization boundary.

#### [M-1] MEDIUM -- API Key Masking Uses Pattern Matching, Misses Non-Standard Formats

**File**: `api_keys.go`
**Functions**: `MaskKey`, key detection regex

Key masking uses regex patterns (`sk-...`, `key-...`, etc.). Custom vendor tokens (hex strings, UUIDs, JWTs used as API keys) are not caught. The `config_vendor.go` `resolveEndpointKey()` resolves keys from env vars but the debug logging in `config_keys.go` could leak them before masking is applied.

**Recommendation**: Default to masking any opaque string value >16 chars in sensitive fields. Use field-level annotations instead of value-pattern matching.

#### [M-2] MEDIUM -- Config File Permissions Not Enforced on Load

**File**: `config.go`

The config loader reads `~/.ggcode/ggcode.yaml` without checking file permissions. If world-readable (0644), any local user can read API keys. `config_save.go` writes with 0600 (good), but existing files from older versions may have relaxed permissions.

**Recommendation**: Warn or refuse to load if config file has group/world-readable permissions.

#### [M-3] MEDIUM -- Anthropic Bootstrap Stores OAuth Token in Config

**File**: `anthropic_bootstrap.go` (288 lines)
**Function**: `BootstrapAnthropicOAuth`

The Anthropic bootstrap flow obtains an OAuth token and stores it back into the config struct (`ep.APIKey = "oauth:..."`). If the config is then saved via `saveConfig()`, the OAuth token is written to disk in the config file. While the save uses 0600 permissions, the token persists in plaintext YAML.

**Recommendation**: Store OAuth tokens in the dedicated token cache (`internal/auth/`) instead of in the config file.

#### [L-1] LOW -- A2A Override File Not Validated for Schema Correctness

**File**: `a2a_override.go`

The `.ggcode/a2a.yaml` override is merged without schema validation. A malformed override could set `allow_unauthenticated: true` alongside auth methods, creating ambiguity.

**Recommendation**: Validate the merged config: if any auth method is configured, reject `allow_unauthenticated: true` unless explicitly confirmed.

#### [L-2] LOW -- Instance Delta Marshals Sensitive Fields to Disk

**File**: `instance_delta.go`

The instance delta mechanism computes minimal diff for per-instance config overrides. The delta may include `api_key` or `extra_headers` fields, which are written to `.ggcode/instance/` YAML files. These files use 0644 permissions (not 0600).

**Recommendation**: Write instance delta files with 0600 permissions, same as the main config.

---

### 2. internal/auth/ -- OAuth2, OIDC, JWT, Token Cache

Files reviewed: `a2a_oauth.go` (933), `a2a_token_cache.go` (142), `a2a_presets.go` (141), `claude_oauth.go` (444), `copilot.go` (227), `store.go` (156), `pkce.go` (47).

#### [H-1] HIGH -- JWKS Cache Has No Stale-While-Revalidate Fallback

**File**: `a2a_oauth.go` lines ~590-700
**Functions**: `fetchJWKS`, `validateJWTWithJWKS`

JWKS keys are fetched and held in memory only. On restart or JWKS endpoint failure, the cache is empty and **all JWT validation fails**, locking out all A2A clients. The OIDC spec (RFC 7517) recommends keeping stale keys as fallback. The `jwksCache` struct has `keys`, `expiry`, and `mu` but no `lastGood` field.

**Recommendation**: Persist JWKS keys to `~/.ggcode/jwks-cache/{issuer_hash}.json`. On fetch failure, fall back to last known good keys with a TTL warning.

#### [H-2] HIGH -- Token Cache File Race Between Multiple ggcode Instances

**File**: `a2a_token_cache.go`, `store.go`

Token cache uses `{provider}-{clientID[:12]}.json` as filename. Two ggcode instances sharing the same provider+clientID race on the same file. The write uses atomic rename (`os.Rename` in `store.go` line 155), but read-then-write is not transactional:
1. Instance A reads token file, gets refresh_token_A
2. Instance B reads token file, gets refresh_token_A
3. Instance A refreshes, writes new token with refresh_token_B
4. Instance B refreshes with stale refresh_token_A (may fail), overwrites with refresh_token_C
5. Instance A's refresh_token_B is lost

**Recommendation**: Add PID or random instance suffix to cache filename, or use `flock` for file locking.

#### [M-4] MEDIUM -- OIDC Discovery Failure Produces Generic Error

**File**: `a2a_oauth.go`

When OIDC discovery (`/.well-known/openid-configuration`) fails, error messages do not distinguish network failure from DNS failure from invalid response. This makes debugging misconfigured OIDC providers very difficult.

**Recommendation**: Wrap errors with URL attempted and HTTP status code.

#### [M-5] MEDIUM -- Claude OAuth Callback Server Port Not Randomized

**File**: `claude_oauth.go` lines ~415-435
**Function**: `startCallbackServer`

The local OAuth callback server binds to a port from a small range (sequential scan for available port). On shared machines, an attacker could pre-bind those ports and intercept the OAuth callback authorization code.

**Recommendation**: Use port 0 (OS-assigned random port) exclusively for the callback server.

#### [L-3] LOW -- Copilot Token Refresh Has No Retry Backoff

**File**: `copilot.go`

The Copilot token refresh (`RefreshToken`) retries on failure but uses a fixed short interval rather than exponential backoff. Under sustained API failures, this creates unnecessary load.

**Recommendation**: Use exponential backoff with jitter for token refresh retries.

---

### 3. internal/mcp/ -- JSON-RPC, Process Lifecycle, OAuth

Files reviewed: `client.go` (904), `oauth.go` (914), `install.go` (371), `migration.go` (221), `adapter.go` (118), `jsonrpc.go` (96), `presets.go` (17), `command_process_unix.go` (15), `command_process_other.go` (8).

#### [H-3] HIGH -- MCP Process stdout Reader May Block Indefinitely on Subprocess Crash

**File**: `client.go` lines ~400-500
**Functions**: `readLoop`, `sendRequest`, `Close`

The MCP client communicates with subprocess-based MCP servers via stdin/stdout using the LSP-style `Content-Length` framing protocol. The reader goroutine calls `reader.Peek(1)` which blocks on stdout. When the subprocess crashes, if the process's stdout pipe is not properly closed (e.g., the process is killed but pipe fd is still held by the OS), the reader goroutine may block indefinitely.

The `Close()` method calls `c.cancel()` which closes `c.stdin`, but closing stdin has no effect on the stdout reader. The `cmd.Process.Kill()` is only called in some error paths.

**Recommendation**: In `Close()`, always call `cmd.Process.Kill()` after closing stdin. Alternatively, use `cmd.Wait()` in a separate goroutine and cancel the reader when Wait completes.

#### [M-6] MEDIUM -- MCP OAuth Token Refresh Not Thread-Safe

**File**: `oauth.go` lines ~160-250
**Function**: `getToken`, `refreshToken`

The `OAuthHandler.getToken()` checks `info.ExpiresAt` and then calls `refreshToken()`. Two concurrent tool calls detecting an expired token will both initiate a refresh, wasting resources and potentially hitting provider rate limits. The `mu` mutex is used for client registration state but not for token refresh serialization.

**Recommendation**: Use `sync.Once` or a dedicated mutex with "refresh in progress" flag to coalesce concurrent refresh attempts.

#### [M-7] MEDIUM -- MCP Install Executes Arbitrary Commands from Registry

**File**: `install.go`
**Functions**: `Install`, `ParseInstallSpec`

`ParseInstallSpec` parses an install spec string (e.g., `"playwright stdio npx -y @playwright/mcp"`) and constructs an `exec.Cmd` with the specified command. The function is called with user-provided strings. While `normalizeServerName` sanitizes the name, the command itself is not validated. A malicious spec like `"evil stdio bash -c 'rm -rf /'"` would be executed.

**Recommendation**: Validate install specs against an allowlist of known safe commands (npx, uvx, etc.) or at least warn the user before executing arbitrary commands.

#### [L-4] LOW -- MCP Process Group Kill Only on Unix

**File**: `command_process_unix.go`, `command_process_other.go`

On Unix, MCP server processes are started in a new session (`Setsid: true`), allowing clean process group termination. On Windows (`command_process_other.go`), no equivalent is set, so child MCP server processes may orphan when the parent exits.

**Recommendation**: Use Windows Job Objects to tie child process lifetime to the parent.

---

### 4. internal/a2a/ -- Multi-Auth, Token Introspection, Registry

Files reviewed: `server.go` (818), `handler.go` (726), `client.go` (596), `registry.go` (471), `mdns.go` (451), `types.go` (444), `mcp_bridge.go` (304), `remote_tool.go` (242), `ip.go` (111), `mdns_proc_unix.go` (16), `mdns_proc_windows.go` (16), `test_handler.go` (11).

#### [H-4] HIGH -- A2A Task State Machine Has No Per-Task Concurrency Guard

**File**: `handler.go` lines ~50-300
**Function**: `updateStatus`, task state transitions

Task state transitions (`submitted -> working -> completed`/`failed`/`canceled`) use a map-level mutex (`h.mu`) but release it between reading current state and writing new state. The typical pattern is:
```go
h.mu.Lock()
t := h.tasks[id]
h.mu.Unlock()
// ... do work ...
h.mu.Lock()
t.Status = newState
h.mu.Unlock()
```

Two concurrent `message/send` calls for the same task can both read `working`, both transition to `completed`, and the second overwrites the first's `result` and `outputArtifacts`.

**Recommendation**: Add per-task mutex (`sync.Mutex` embedded in `Task` struct) or use atomic compare-and-swap for state transitions. Ensure the entire read-check-write sequence is atomic per task.

#### [M-8] MEDIUM -- A2A Server Allows Unauthenticated localhost Without Explicit Config

**File**: `server.go` lines ~88-100
**Function**: `a2aMiddleware`

When no auth is configured, the A2A server defaults to binding `127.0.0.1` and allowing all requests from localhost without any authentication. The `allow_unauthenticated` flag defaults to `false`, yet localhost is still allowed. Any local process (including browsers via SSRF) can invoke A2A methods.

**Recommendation**: When `allow_unauthenticated` is false and no auth is configured, require at least a per-invocation token for localhost connections.

#### [M-9] MEDIUM -- A2A SSE Stream Does Not Handle Backpressure

**File**: `server.go` lines ~400-500
**Function**: SSE streaming handler

The SSE handler uses `fmt.Fprintf(w, ...)` with `http.Flusher`. If the client reads slowly, the server-side buffer grows unbounded. There is no flow control, write deadline, or buffer size limit per SSE connection. A misbehaving or slow client could cause memory pressure.

**Recommendation**: Add a write deadline or buffer size limit per SSE connection. Close connections that stall beyond a threshold.

#### [M-10] MEDIUM -- A2A mDNS Broadcasts Instance Info Including Port Without Auth Requirement

**File**: `mdns.go`
**Function**: `Broadcast`, `Browse`

mDNS broadcasts include the instance ID, hostname, port, and capabilities. If `lan_discovery` is enabled, any device on the LAN can discover the A2A server's port and attempt connections. Combined with M-8 (localhost allowed without auth), this expands the attack surface.

**Recommendation**: Ensure mDNS is only advertised when auth is configured. Refuse to broadcast if `allow_unauthenticated` is true or no auth is set.

#### [L-5] LOW -- A2A Registry Uses PID-Based Instance Detection, No Stale Entry Cleanup

**File**: `registry.go`

The local registry detects running instances by checking if their PID is still alive. On process crash without cleanup, stale registry entries may persist until the PID is recycled. A new unrelated process could reuse the PID, causing the registry to return stale instance info.

**Recommendation**: Add a timestamp to registry entries and consider a health-check probe for stale entries.

---

### 5. internal/harness/ -- Task Management, Worktrees, Release

Files reviewed: `release.go` (931), `run.go` (660), `task.go` (403), `worktree.go` (396), `router.go` (390), `events.go` (469), `monitor.go` (361), `project.go` (334), `promotion.go` (297), `context_report.go` (241), `context_suggest.go` (251), `config.go` (199), `doctor.go` (218), `worker.go` (162), `templates.go` (178), `llm_classifier.go` (214), `delivery.go` (122), `auto_init.go` (116), `auto_run.go` (111), `drift.go` (71), `inbox.go` (179), `check.go` (266), `review.go` (100), `run_service.go` (186), `context.go` (40), `context_config.go` (57), `context_runtime.go` (56), `context_userinput.go` (42), `helpers.go` (14), `gc.go` (105).

#### [M-11] MEDIUM -- Harness Worktree Cleanup Not Performed on Context Cancellation

**File**: `worktree.go`
**Function**: `CreateWorktree`, cleanup logic

When creating git worktrees for isolated task execution, cleanup (`git worktree remove`) is deferred but may not execute if the parent context is cancelled. Abandoned worktrees accumulate in `.ggcode/worktrees/` and are never cleaned up automatically. The `gc.go` does have cleanup logic, but only for tasks older than the retention period -- not for orphaned worktrees from cancelled contexts.

**Recommendation**: Register cleanup in a finalizer or add a separate worktree garbage collection pass for orphaned directories.

#### [M-12] MEDIUM -- Harness Release Executes Arbitrary Git Commands Without Sandboxing

**File**: `release.go` lines ~200-500
**Functions**: `UpdateReleaseWaveStatus`, `AdvanceReleaseWaveRollout`

The release pipeline executes git commands (`exec.Command("git", ...)`) in the project directory. The `workingDir` is taken from the project config. While the project path is validated at load time, a crafted project path could cause git to operate on unexpected directories. The commands are run without resource limits or timeout.

**Recommendation**: Add context timeout to all git commands in release pipeline. Validate working directory existence before each command.

#### [L-6] LOW -- Harness JSON Event Files Have No Size Limit

**File**: `events.go`

Harness events are appended to JSON files under `.ggcode/harness/`. For long-running workspaces, these files grow without bound. No rotation or compaction is performed. The `gc.go` handles archival but not active file rotation.

**Recommendation**: Add max file size or event count limits with rotation.

#### [L-7] LOW -- Harness Worker Pool Has No Timeout per Task

**File**: `worker.go`

The worker pool processes tasks sequentially but has no per-task timeout. A stalled task blocks the worker indefinitely. The `run.go` uses a context but it's the parent context, not a per-task deadline.

**Recommendation**: Add configurable per-task timeout (default 30 min).

---

### 6. internal/cron/ -- Scheduled Jobs

Files reviewed: `parser.go` (220), `scheduler.go` (206).

#### [M-13] MEDIUM -- Cron Expression Parser Accepts Unreachable Schedules Silently

**File**: `parser.go`

The cron parser handles standard 5-field expressions and `*/N` step values. Edge cases like `0 0 31 2 *` (February 31st) are accepted without warning. The scheduler will simply never fire, which is silent failure. Also, `0 0 0 * *` with day-of-week=0 is accepted even though some systems use 0=Sunday and others 0=Monday.

**Recommendation**: Validate that the expression can produce at least one future firing time. Warn on unreachable dates.

#### [L-8] LOW -- Scheduler Map Iteration Order Non-Deterministic

**File**: `scheduler.go`

The `tick()` method iterates over `s.jobs` map. If two jobs have the same fire time, which fires first depends on map ordering. This could affect determinism in testing.

**Recommendation**: Sort jobs by ID or creation time before firing.

---

### 7. internal/tunnel/ -- Event Persistence, Replay, WebSocket, Encryption

Files reviewed: `broker.go` (1361), `relay_client.go` (565), `protocol.go` (337), `session.go` (150), `crypto.go` (73), `qrcode.go` (39), `reasoning.go` (19).

#### [C-2] CRITICAL -- Tunnel Session Token Serves as Both Auth Credential and Encryption Key

**File**: `session.go` lines 53-111, `crypto.go`
**Functions**: `NewTunnelSession`, `Encrypt`, `Decrypt`

The tunnel session token is generated via `crypto.rand` (good), but serves dual purpose:
1. **Authentication**: embedded in the WebSocket connect URL (`?token=...`) and rendered as QR code via `qrcode.go`
2. **Encryption**: used as the AES-GCM key via `DeriveKey()` in `crypto.go`

If the URL or QR code is intercepted (screenshot, shoulder surfing, log file, browser history), all past and future tunnel messages can be decrypted. The `Encrypt` function uses `crypto/rand` for nonces (good) and AES-GCM (good), but the key material is exposed in the clear.

Furthermore, `relay_client.go` line 564 exposes `Token()` via a public getter, making the key accessible to any code that holds a `RelayClient`.

**Recommendation**: Separate authentication token from encryption key. Use a key exchange protocol (e.g., Diffie-Hellman over the relay) to derive the encryption key. The QR code should only contain the auth token, not the encryption key.

#### [M-14] MEDIUM -- Tunnel Broker Replay Event Ordering Not Verified Across Reconnects

**File**: `broker.go` lines ~300-600
**Functions**: `startClientReplaySync`, `replayEvents`

The broker replays canonical events on reconnect. While `snapshot_reset` correctly does not consume an event ordinal, the replay provider returns events from memory without verifying monotonic ID ordering. If the broker was restarted, in-memory replay data is lost and falls back to disk-based JSONL, which may have incomplete events at the tail.

**Recommendation**: Verify monotonic event ID ordering during replay. Detect and report gaps.

---

### 8. internal/im/ -- Adapter Lifecycle, Echo Suppression, Slash Commands

Files reviewed: `runtime.go` (1113), `daemon_bridge.go` (1425), `bindings.go` (242), `adapters.go` (279), `emitter.go` (428), `tool_status.go` (609), `history.go` (64), `types.go` (336), `proxy.go` (190), `instance_detect.go` (269), `qq_adapter.go` (1538), `tg_adapter.go` (1177), `discord_adapter.go` (985), `slack_adapter.go` (1028), `dingtalk_adapter.go` (781), `feishu_adapter.go` (889), `wecom_adapter.go` (864), `wechat_adapter.go` (649), `whatsapp_adapter.go` (620), `pc_adapter.go` (797), `irc_adapter.go` (598), `matrix_adapter.go` (660), `nostr_adapter.go` (626), `twitch_adapter.go` (463), `mattermost_adapter.go` (499), `dummy_adapter.go` (122), `dummy_server.go` (373), `dummy_metrics.go` (173), `ask_user_format.go` (219), `ask_user_parse.go` (211), `image_extract.go` (262), `message_split.go` (130), `pairing.go` (150), `pc_client.go` (59).

#### [M-15] MEDIUM -- IM Adapter Tokens Stored in Binding File in Plaintext

**File**: `bindings.go` lines 207-225, `instance_detect.go`

IM adapter configs (including access tokens for QQ, Telegram, Discord, etc.) are stored in the binding JSON file under `.ggcode/im-bindings/`. The binding file is written with standard permissions (no 0600 enforcement visible). Tokens for QQ (access_token), Telegram (bot_token), Discord (bot_token), Slack (bot_token), DingTalk (access_token), Feishu (app_id/app_secret), WeCom (corp_secret), etc. are all stored in plaintext JSON.

**Recommendation**: Encrypt sensitive adapter fields at rest, or at minimum write binding files with 0600 permissions.

#### [M-16] MEDIUM -- IM Binding Store Migration Writes on Every Read

**File**: `bindings.go` lines 207-225
**Function**: `readAllLocked`

The JSON file binding store migrates legacy keys on every `readAllLocked()` call. The migration calls `writeAllLocked()` inside the read path, meaning every read operation potentially triggers a file write. This is wasteful and could cause unnecessary disk I/O under high-frequency status polling.

**Recommendation**: Track whether migration has been performed (sentinel flag) and only migrate once.

#### [L-9] LOW -- IM Adapter Mute State Lost on Daemon Restart

**File**: `runtime.go`

Adapter mute state (set via `/muteim`, `/muteall`, `/muteself`) is in-memory only and lost on daemon restart. While documented, this can surprise users who muted an adapter expecting persistence.

**Recommendation**: Consider persisting mute state to the binding store.

#### [L-10] LOW -- IM Proxy CONNECT Does Not Validate Target Host

**File**: `proxy.go`
**Function**: `connectToProxy`

The SOCKS/HTTP proxy connection (`connectToProxy`) connects to the configured proxy address but does not validate the target hostname in CONNECT requests. A malicious proxy could redirect connections.

**Recommendation**: Validate target hostname against an allowlist or at least reject private IP ranges.

---

### 9. internal/webui/ -- WebSocket, REST API, CSRF, XSS

Files reviewed: `server.go` (391), `server_handlers.go` (1146), `server_websocket.go` (298), `server_static.go` (58), `auth.go` (49), `embed.go` (20), `tui_bridge.go` (127).

#### [M-17] MEDIUM -- WebUI Config Update Endpoint Accepts Arbitrary Fields

**File**: `server_handlers.go` lines ~100-200
**Function**: `handleConfig` (POST)

The `POST /api/config` endpoint deserializes the request body into a partial config struct and applies it. While it only applies known fields (`vendor`, `endpoint`, `model`, `mode`, `max_iterations`), there is no input validation on values. For example, `max_iterations` could be set to a negative number or an extremely large value.

**Recommendation**: Validate all input values (ranges, format) before applying.

#### [M-18] MEDIUM -- WebUI MCP Server Config Endpoint Exposes Sensitive Fields

**File**: `server_handlers.go` lines ~439-650
**Functions**: `handleMCPList`, `handleMCPDetail`

The `GET /api/mcp` endpoint returns MCP server configs including `env` map (which may contain expanded `${ENV_VAR}` secrets) and `headers` (which may contain auth tokens). While the GET handler masks API keys in some places, the `env` and `headers` fields are returned as-is.

**Recommendation**: Mask values in `env` and `headers` fields that contain expanded env vars.

#### [L-11] LOW -- WebUI Auth Token Lost on Page Refresh

**File**: `server_static.go` lines 13-35

The auth token is extracted from the URL hash and stored in JavaScript memory only. A page refresh loses the token. The approach is reasonable for localhost-only serving, but degrades UX.

**Recommendation**: Consider using `sessionStorage` with TTL for the token.

#### [L-12] LOW -- WebUI MCP Server DELETE Endpoint Accepts Arbitrary Name

**File**: `server_handlers.go` lines 439-458

The `DELETE /api/mcp/{name}` endpoint extracts the server name from the URL path without sanitization. The actual deletion uses a map lookup (not file paths), so the practical risk is minimal.

**Recommendation**: Validate that the name contains only alphanumeric characters, hyphens, and underscores.

---

### 10. internal/daemon/ -- Background Forking, Follow Display

Files reviewed: `follow.go` (824), `background.go` (190), `background_unix.go` (28), `background_windows.go` (18).

No significant security findings. The daemon package is well-structured:

- Background forking correctly writes PID files with atomic rename (`background.go` line 117-155).
- Follow display uses `bufio.Scanner` with line-length limits to prevent memory exhaustion.
- Terminal output properly handles ANSI escape sequences with reset.
- Unix background uses `Setpgid: true` for clean process group management.
- Windows background `checkProcessAlive` always returns nil (line 18), meaning the daemon cannot detect if the child process died on Windows. This is a **low** robustness issue, not a security issue.

---

### 11. internal/commands/ -- Slash Command Registry

Files reviewed: `manager.go` (177), `loader.go` (196), `bundled.go` (135), `command.go` (96), `usage.go` (134), `disabled_state.go` (115).

No significant findings. The command registry uses proper mutex protection for concurrent registration and lookup. Command loading from YAML files validates structure. Usage formatting is correct.

One minor note: `loader.go` reads YAML files from `~/.ggcode/skills/` without schema validation, but the loaded data is only used as prompt templates, not executed.

---

### 12. internal/memory/ -- Project Memory Loading

Files reviewed: `init.go` (228), `project.go` (167), `auto.go` (122).

No significant findings. Memory file loading properly:

- Checks for `.git` directory markers to scope scans.
- File content is read with reasonable defaults (no explicit size limit, but capped by `read_file` tool at 2000 lines).
- Auto-memory persistence uses atomic file writes.
- The `auto.go` `sanitizeFileName` properly strips path traversal characters.

---

### 13. internal/lsp/ -- LSP Client Integration

Files reviewed: `client.go` (1053), `discovery.go` (964), `operations.go` (224), `session.go` (382), `e2e_test.go` (234), `integration_test.go` (484).

No significant security findings. The LSP client correctly:

- Implements the Language Server Protocol with `Content-Length` headers.
- Manages process lifecycle with context cancellation and `cmd.Process.Kill()`.
- Session management with per-client mutexes and idle timeout (5 minutes).
- Discovery properly finds language servers on common platforms.
- `session.go` uses proper mutex for concurrent diagnostic updates.

---

## Consolidated Findings Table

| ID | Severity | Package | Summary |
|----|----------|---------|---------|
| C-1 | **Critical** | config | Expanded env vars (API keys, secrets) leak through WebUI REST API endpoints |
| C-2 | **Critical** | tunnel | Session token is both auth credential AND encryption key, exposed in URL/QR code |
| H-1 | **High** | auth | JWKS cache has no stale-while-revalidate; endpoint failure locks out all A2A clients |
| H-2 | **High** | auth | Token cache file race between multiple ggcode instances sharing provider+clientID |
| H-3 | **High** | mcp | MCP process stdout reader may block indefinitely on subprocess crash |
| H-4 | **High** | a2a | Task state machine has no per-task concurrency guard; concurrent transitions can race |
| M-1 | Medium | config | API key masking uses pattern matching, misses non-standard formats |
| M-2 | Medium | config | Config file permissions not enforced on load |
| M-3 | Medium | config | Anthropic bootstrap stores OAuth token in config file |
| M-4 | Medium | auth | OIDC discovery failure produces generic error |
| M-5 | Medium | auth | Claude OAuth callback server port not randomized |
| M-6 | Medium | mcp | MCP OAuth token refresh not thread-safe |
| M-7 | Medium | mcp | MCP install executes arbitrary commands from install specs |
| M-8 | Medium | a2a | Unauthenticated localhost allowed without explicit config |
| M-9 | Medium | a2a | SSE stream does not handle backpressure |
| M-10 | Medium | a2a | mDNS broadcasts instance info without requiring auth |
| M-11 | Medium | harness | Worktree cleanup not performed on context cancellation |
| M-12 | Medium | harness | Release executes arbitrary git commands without sandboxing/timeout |
| M-13 | Medium | cron | Cron parser accepts unreachable schedules silently |
| M-14 | Medium | tunnel | Replay event ordering not verified across reconnects |
| M-15 | Medium | im | IM adapter tokens stored in binding file in plaintext |
| M-16 | Medium | im | Binding store migration writes on every read |
| M-17 | Medium | webui | Config update endpoint lacks input validation |
| M-18 | Medium | webui | MCP server config endpoint exposes env/headers with secrets |
| L-1 | Low | config | A2A override file not validated for schema correctness |
| L-2 | Low | config | Instance delta files written with 0644 permissions |
| L-3 | Low | auth | Copilot token refresh has no retry backoff |
| L-4 | Low | mcp | MCP process group kill only on Unix |
| L-5 | Low | a2a | Registry uses PID-based detection, no stale entry cleanup |
| L-6 | Low | harness | JSON event files have no size limit |
| L-7 | Low | harness | Worker pool has no timeout per task |
| L-8 | Low | cron | Scheduler map iteration order non-deterministic |
| L-9 | Low | im | Adapter mute state lost on daemon restart |
| L-10 | Low | im | Proxy CONNECT does not validate target host |
| L-11 | Low | webui | Auth token lost on page refresh |
| L-12 | Low | webui | MCP server DELETE endpoint accepts arbitrary name |

---

## Positive Observations

1. **Constant-time comparison**: A2A API key validation uses `crypto/subtle.ConstantTimeCompare`.
2. **Atomic file writes**: All config/token/binding persistence uses write-to-tmp + `os.Rename`.
3. **Encrypted tunnel transport**: AES-GCM with `crypto/rand` nonces.
4. **Per-client token isolation**: OAuth token cache uses `{provider}-{clientID[:12]}` filenames.
5. **WebSocket write isolation**: WebUI uses per-connection buffered channels (cap 256) preventing concurrent read/write.
6. **JSON-RPC 2.0 compliance**: Both MCP and A2A correctly implement spec error codes.
7. **Process isolation**: MCP server subprocesses started with `Setsid: true` on Unix.
8. **TOCTOU-safe run slot**: `DaemonBridge.SendUserMessage` claims run slot under single mutex lock.
9. **Proper mutex usage**: Most shared state protected by `sync.RWMutex` with correct RLock/Lock.
10. **JWRS key rotation**: OIDC JWKS polling with automatic key rotation (when fetch succeeds).
11. **PKCE for OAuth**: OAuth2 flows use PKCE (`crypto/rand` verifier, SHA-256 challenge).
12. **Cryptographic RNG throughout**: Nonces, tokens, state params all use `crypto/rand`.
13. **Content-Security-Policy consideration**: WebUI serves SPA from embedded FS with hash-based auth.
14. **Proper LSP protocol**: Content-Length framing, request/response ID matching, cancellation support.

---

## Recommendations Priority

1. **Immediate** (Critical):
   - **C-2**: Separate tunnel encryption key from auth token. Use key exchange protocol.
   - **C-1**: Create sanitized config view for WebUI REST endpoints. Never return expanded env vars or raw key material.

2. **Soon** (High):
   - **H-1**: Persist JWKS keys to disk with stale-while-revalidate fallback.
   - **H-2**: Add instance isolation to token cache files (PID suffix or file locking).
   - **H-3**: Always `cmd.Process.Kill()` in MCP client Close(). Monitor subprocess via `cmd.Wait()`.
   - **H-4**: Add per-task mutex for A2A task state transitions.

3. **Next Sprint** (Medium):
   - Address MCP OAuth refresh coalescing (M-6).
   - Add SSE backpressure limits (M-9).
   - Restrict mDNS to auth-configured instances only (M-10).
   - Validate MCP install spec commands (M-7).
   - Encrypt IM adapter tokens at rest (M-15).
   - Add WebUI config input validation (M-17).
   - Mask sensitive fields in MCP server config API (M-18).

4. **Backlog** (Low):
   - Port randomization for OAuth callbacks.
   - Windows process group management.
   - Cron schedule validation.
   - IM mute state persistence.
   - Worktree GC for cancelled contexts.
