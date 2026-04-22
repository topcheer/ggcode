# ggcode Code Review & Optimization Plan
**Date:** 2026-04-21  
**Reviewer:** main-agent

---

## 🚨 Critical Issues (Must Fix)

### 1. ACP e2e test deadlock
- **File:** `internal/acp/acp_e2e_test.go`
- **Issue:** `TestE2EPermissionRequestResponse` hangs — `SendRequest` and handler compete for same scanner
- **Root cause:** Pipe transport doesn't support multiplexed reads; `handler.Run()` goroutine and `SendRequest` both read from agentTransport
- **Fix:** Separate transport for bidirectional requests, or don't start handler for transport-level tests

### 2. Harness tests failing (pre-existing)
- **Files:** `internal/harness/harness_test.go`, `internal/harness/unit_test.go`
- **Issue:** Multiple test failures — likely SQLite schema/state issues
- **Priority:** P0 — harness is core workflow engine

### 3. Tool tests failing  
- **File:** `internal/tool/*_test.go`
- **Issue:** New tools (cron, swarm_task, etc.) may have incomplete test setup

### 4. Provider tests failing
- **File:** `internal/provider/`
- **Issue:** OpenAI/Anthropic test failures — likely API mock issues

---

## 📊 Test Coverage Report

| Package | Status | Coverage | Priority |
|---------|--------|----------|----------|
| safego | ✅ OK | 100% | Done |
| swarm | ✅ OK | 93% | Good |
| task | ✅ OK | 93.9% | Good |
| permission | ✅ OK | 75.1% | Good |
| agent | ✅ OK | 77.7% | Good |
| knight | ✅ OK | 71.1% | P2 |
| cron | ✅ OK | 69.1% | P1 — need more edge cases |
| config | ✅ OK | 61.4% | P2 |
| a2a | ✅ OK | 63.8% | P1 — need e2e with real provider |
| tui | ✅ OK | 50.6% | P1 — stream batch, panels need coverage |
| mcp | ✅ OK | 43.3% | P0 — low coverage, critical subsystem |
| acp | ❌ FAIL | N/A | P0 — deadlock + no e2e |
| tool | ❌ FAIL | N/A | P0 — new tools need tests |
| harness | ❌ FAIL | N/A | P0 — core engine |
| provider | ❌ FAIL | N/A | P0 — streaming tests |
| im | ❌ FAIL | N/A | P1 — dummy adapter test |

### No tests at all:
- `internal/daemon/` (4 files)
- `internal/im/stt/` (4 files — speech-to-text)
- `internal/version/` (1 file)

---

## 🏗️ Architecture Issues (Long-term)

### 1. TUI is a monolith (70 files, ~30k LOC)
- **Problem:** All TUI code in single package — model, update, view, commands, panels, completion, markdown, etc.
- **Proposal:** Split into sub-packages:
  - `tui/core` — Model, Update loop, Messages
  - `tui/panels` — Harness panel, Agent detail, Inspector
  - `tui/render` — Markdown, layout, spinner
  - `tui/input` — Completion, keybindings, mouse

### 2. IM adapters lack unified interface
- **Problem:** Each adapter (QQ, TG, Discord, Slack, DingTalk, Feishu) duplicates message splitting, error handling, reconnection logic
- **Proposal:** Extract `BaseAdapter` with common retry/split/reconnect logic

### 3. Provider streaming is duplicated
- **Problem:** OpenAI, Anthropic, Gemini each have nearly identical `streamRead` goroutines with `safego.Go`
- **Proposal:** Extract `StreamReadLoop` into `provider/stream.go` shared base

### 4. No structured error types
- **Problem:** Errors are plain `fmt.Errorf` everywhere — can't programmatically handle specific error types
- **Proposal:** Define `internal/errors/` with typed errors: `ErrRateLimit`, `ErrAuthExpired`, `ErrToolNotFound`, etc.

### 5. Config is a god object
- **File:** `internal/config/config.go` — 1500+ lines
- **Problem:** Single struct holds everything: API keys, UI settings, harness config, swarm config, IM settings
- **Proposal:** Split into focused config sections with validation

---

## 📋 Immediate TODO (ordered by priority)

### P0 — Fix all failing tests
- [ ] Fix ACP e2e deadlock (`TestE2EPermissionRequestResponse`)
- [ ] Fix harness test failures
- [ ] Fix tool test failures  
- [ ] Fix provider test failures
- [ ] Fix IM dummy adapter test

### P1 — Improve coverage on critical paths
- [ ] ACP: real e2e test with true LLM provider
- [ ] A2A: real e2e test with multi-agent task flow
- [ ] MCP: client lifecycle, tool discovery, stream handling
- [ ] TUI: stream batch, agent panel, tool status display
- [ ] Swarm: teammate collaboration e2e
- [ ] Knight: benchmark + real e2e with skill execution

### P2 — Test uncovered packages
- [ ] daemon package tests
- [ ] STT (speech-to-text) tests
- [ ] Cron parser edge cases (leap year, timezone, etc.)

### P3 — Architecture improvements
- [ ] Refactor TUI monolith into sub-packages
- [ ] Extract base IM adapter
- [ ] Shared provider streaming loop
- [ ] Structured error types
- [ ] Config split
