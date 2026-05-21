# Full Codebase Review — Round 5

**Date**: 2025-05-21
**Scope**: Full codebase across all internal packages
**Method**: 4 parallel reviewers (TUI, Core, Security, Infra)

---

## Executive Summary

| Severity | TUI | Core | Security | Infra | Total |
|----------|-----|------|----------|-------|-------|
| CRITICAL | 0 | 0 | 1 | 0 | 1 |
| HIGH | 3 | 1 | 3 | 1 | 8 |
| MEDIUM | 10 | 8 | 5 | 12 | 35 |
| LOW | 8 | 11 | 4 | 16 | 39 |

**Most urgent finding**: JWT issuer/audience fallback bypass in A2A auth (CRITICAL).

---

## 1. TUI Layer (`internal/tui/`)

### HIGH
- **Hard-coded Chinese strings** in `model.go:626` — should go through i18n
- **Unlocalized "Tunnel stopped."** in `model_update.go:357`
- **`swarmTextLastNotify` map leak** in `repl.go:303` — map grows unbounded, never cleaned up

### MEDIUM
- i18n bypasses in `view_panels.go` and `inspector_panel.go`
- `tunnelSpawned` map leak — never cleaned up
- Double rendering in `View()` — potential performance issue
- Silent session save errors — failures logged but not surfaced to user
- Permissive approval prefix matching — `strings.HasPrefix(userChoice, "y")` matches "yellow"
- CJK truncation issues — `lipgloss.Width` may not handle fullwidth chars correctly

### LOW
- Inline i18n patterns in `activity_groups.go`
- Duplicated tool filter lists across multiple files
- Non-atomic counters in stats
- `View()` mutating state (rendering side effects)

### Positive
- Strong concurrency discipline via `program.Send()`
- Well-designed batch streaming with `sync.Once`
- API key sanitization
- Proper sub-agent cleanup on interrupt
- Consistent run ID tracking for stale message filtering

---

## 2. Core Agent, Provider, Tool, Config (`internal/agent/`, `internal/provider/`, `internal/tool/`, `internal/config/`, `internal/session/`, `internal/context/`, `internal/memory/`)

### HIGH
- **Atomic rollback data loss** (`multi_file_tools.go:455-467`): When rollback write fails during multi_file_edit, the error is silently discarded (`_ = atomicWriteFile(...)`). User gets no indication of which files couldn't be reverted.

### MEDIUM
- **Autopilot loop guard threshold** hardcoded at 2 — could create confusing UX
- **Gemini duplicate tool IDs** (`gemini.go:186-188`): When `FunctionCall.ID` is empty, falls back to function name, causing duplicate IDs for same tool called twice
- **Token estimation heuristics** — can be off by 20-40% for code-heavy content
- **Bypass mode allows dangerous commands** — `curl`, `pip install` allowed without confirmation
- **Config compound read-modify-write** — mutex protects individual ops but not multi-step sequences
- **Checkpoint non-atomic rewrite** (`session/store.go`): `WriteCheckpoint` truncates then writes — crash mid-write = data loss
- **Microcompact truncates recent tool results** — one-way transformation, no undo
- **Symlink attack in memory loading** (`memory/project.go`): `./GGCODE.md -> /etc/shadow` would read sensitive files

### LOW
- Agent loop runs N+1 iterations instead of N when `max_iterations` is set
- Session index rewritten on every append (O(n) per message)
- Token estimation doesn't account for per-message overhead
- Various minor issues and positive observations

### Positive
- Tool-use/tool-result pairing on cancellation handled correctly
- JSONL append is crash-safe at record level
- Env expansion immune to shell injection
- Summarization TOCTOU race properly addressed
- Multi-file tool parameter validation is thorough
- Legacy config schema explicitly rejected with clear error

---

## 3. Security (`internal/auth/`, `internal/a2a/`, `internal/permission/`, `internal/tunnel/crypto.go`)

### CRITICAL
- **JWT issuer/audience fallback bypass** (`a2a_oauth.go:443-453`): When strict JWT validation fails, code falls back to parsing **without** issuer/audience check. An attacker with a validly-signed JWT from a different issuer can authenticate.

### HIGH
- **A2A auth methods OR-combined** (`server.go:229-271`): Multiple auth methods checked with OR logic — attacker can bypass stronger method by satisfying a weaker one
- **Push notification SSRF** (`server.go:754-793`): Unrestricted URLs fired via `http.DefaultClient` with no validation
- (Third HIGH from earlier review)

### MEDIUM
- **HMAC key uses public clientID** — anyone knowing client ID can forge HMAC JWTs
- **Agent Card MITM enables auth downgrade** — served over plain HTTP in LAN
- **Sandbox path traversal via symlinks** (`sandbox.go`): `filepath.Abs` doesn't resolve symlinks
- **Dangerous command classification gaps** — doesn't detect base64/eval/exec/pipe chains
- **Crypto token truncation** — tokens >32 bytes silently truncated

### LOW
- PID-based instance detection unreliable
- Mode transitions without re-authentication
- Env expansion confirmed safe

### Positive
- keys.env permissions correctly set (0600 with forced chmod)
- Plaintext API key detection and migration
- AES-GCM AEAD construction is correct with fresh random nonces
- Env expansion is safe from injection

---

## 4. Infrastructure (`internal/daemon/`, `internal/im/`, `internal/webui/`, `internal/harness/`, `internal/mcp/`, `internal/subagent/`, `internal/swarm/`)

### HIGH
- **Harness: No cycle detection in task dependencies** (`task.go:282-319`): If task A depends on B and B depends on A, both stuck in `TaskBlocked` forever with no error. No topological sort or cycle detection.

### MEDIUM
- **Daemon: No terminal state restoration on crash** (`background.go`, `follow.go`): SIGKILL/OOM leaves terminal in broken state (raw mode, alternate screen buffer)
- **Harness: Event store — no corruption recovery** (`events.go`): Partial lines from crash cause parse errors, no repair mechanism
- **Harness: Promotion race condition** (`promotion.go:41-65`): Read-modify-write not atomic under mutex — concurrent promotions could double-merge
- **Harness: Worktree cleanup on failure** (`worktree.go`): Requires explicit `CleanupStaleWorktrees()` call, no auto-cleanup on startup
- **IM: Outbound routing fire-and-forget** (`runtime_bindings.go:634-660`): Message delivery failures not retried or queued — messages silently lost on flaky connections
- **MCP: Process management zombie risk** (`client.go:262-273`): 3-second wait on Close could leave orphan zombie processes
- **MCP: WebSocket transport — no reconnection** (`client.go:470-501`): Temporary network hiccup permanently breaks WebSocket MCP servers
- **Swarm: Idle runner goroutine accumulation** (`swarm/idle_runner.go`): Long-lived daemons with many teams could accumulate idle goroutines
- **Update: No post-update version verification** (`update.go:349-364`): Failed partial update could result in older/corrupt binary without warning
- **Update: Helper runs detached — no feedback** (`update.go:173-176`): No mechanism to report helper failure to user
- **WebUI: No CORS configuration** (`server.go`): Acceptable for localhost-only but limits proxy/extension use

### LOW
- Daemon: Zombie process prevention adequate (Setpgid)
- IM: Echo suppression per-channel design is sound
- IM: Adapter lifecycle — well-structured 3-step shutdown
- IM: Dedup map memory bounded (5-min TTL)
- WebUI: WebSocket goroutine leak protection present
- WebUI: REST API input validation minimal but adequate
- WebUI: Auth token generation cryptographically sound
- MCP: JSON-RPC ordering correct (single mutex)
- MCP: OAuth 2.1 token refresh implemented correctly
- SubAgent: Semaphore correctness — well implemented
- SubAgent/Swarm: Cancel propagation cascaded correctly
- Swarm: Team/teammate lifecycle — clean cleanup
- SubAgent: Graceful panic recovery in runner
- Install: Binary replacement atomic with temp file
- Install: Checksum verification SHA-256
- Install: Checksum from same source as binary (standard for GitHub Releases)

---

## Priority Fixes (Recommended Order)

### Immediate (CRITICAL/HIGH)
1. **Fix JWT fallback** — Remove `jwt.ParseWithClaims` without issuer/audience check in `a2a_oauth.go`
2. **Fix A2A auth method combination** — Use AND logic or require specific scheme match
3. **Fix push notification SSRF** — Validate URLs against private IP ranges
4. **Fix atomic rollback error reporting** — Log/report failed rollbacks in `multi_file_edit`
5. **Fix i18n hard-coded strings** — Move Chinese strings and "Tunnel stopped." to i18n catalogs
6. **Fix map leaks** — Clean up `swarmTextLastNotify` and `tunnelSpawned` maps
7. **Add harness task dependency cycle detection** — DFS cycle check at CreateTask/SaveTask

### Short-term (MEDIUM)
8. Fix sandbox symlink traversal — use `filepath.EvalSymlinks()`
9. Fix memory loading symlink attack — use `os.Lstat` + check `Mode()&os.ModeSymlink`
10. Fix Gemini duplicate tool IDs — generate unique IDs when API doesn't provide them
11. Make checkpoint writes atomic — temp file + rename pattern
12. Add bypass-mode command restrictions — "high-danger" category not overridable
13. Fix approval prefix matching — use exact match for "y"/"n" choices
14. Fix harness promotion race — extend mutex over entire PromoteTask operation
15. Add auto worktree cleanup on harness startup
16. Add MCP WebSocket reconnection logic
