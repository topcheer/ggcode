# Architecture & Dependency Audit Report

**Codebase**: ggcode (~145k LOC non-test, ~108k LOC test, 41 internal packages, 440 source files)
**Auditor**: arch-auditor (tm-6)
**Date**: 2026-05-10

---

## Summary

| Severity | Count |
|----------|-------|
| High     | 4     |
| Medium   | 10    |
| Low      | 8     |
| **Total**| **22**|

---

## 1. Package Dependency Graph

### Finding 1.1: Zero direct circular dependencies (CLEAN)
- **Severity**: Low (positive finding)
- **Category**: Architecture
- **Description**: The dependency graph is a clean DAG with no bidirectional import cycles. No internal package imports another internal package that imports it back. The Go compiler enforces this, but the structure is well-designed.
- **Key observation**: The leaf packages (`cost`, `cron`, `diff`, `extract`, `image`, `restart`, `safego`, `task`, `util`, `version`) have zero or near-zero internal imports, forming a stable base layer.

### Finding 1.2: TUI is a "god package" importing 31 of 41 internal packages
- **Severity**: High
- **Category**: Architecture / Coupling
- **File**: `internal/tui/` (43,288 LOC, 107 files)
- **Description**: The TUI package imports 31 out of 41 internal packages. It is the sole consumer of many packages (`chat`, `knight`, `cron`, `update`, `restart`, `hooks`, `daemon` bridge). This makes the TUI package impossible to test in isolation and creates a massive coupling surface.
- **Suggested refactoring**: Extract sub-packages:
  - `internal/tui/panels/` - IM panels (qq, tg, discord, slack, feishu, wechat, whatsapp, signal, irc, matrix, etc.)
  - `internal/tui/harness/` - harness panel and commands
  - `internal/tui/knight/` - knight panel
  - `internal/tui/render/` - shared rendering utilities (tool_labels, view)
  - Keep `internal/tui/` as the core Bubble Tea model only

### Finding 1.3: `internal/context` import alias inconsistency
- **Severity**: Low
- **Category**: Convention
- **File**: Only `internal/agent/agent.go` and `internal/agent/agent_precompact.go` import `internal/context`, both correctly using the `ctxpkg` alias.
- **Description**: The convention is followed where it matters. Only 2 files import the package, both with `ctxpkg` alias. No inconsistency found.

### Finding 1.4: `internal/cost/types.go` — dead code with duplicated type
- **Severity**: Medium
- **Category**: Dead Code / Duplication
- **File**: `internal/cost/types.go` (10 lines) vs `internal/provider/provider.go:75` (TokenUsage struct)
- **Description**: `cost.TokenUsage` is a duplicate of `provider.TokenUsage`, documented as "Defined in the cost package to avoid circular imports." However, **zero files** import the `cost` package. The `cost` package (280 LOC, 4 files) appears to be entirely unused. All consumers use `provider.TokenUsage` directly.
- **Suggested refactoring**: Remove the `internal/cost/` package entirely or actually use it. If the cost-tracking feature is intended, integrate it. Otherwise, delete dead code.

### Finding 1.5: `context -> tool` transitive dependency creates tight coupling
- **Severity**: Medium
- **Category**: Architecture
- **Files**: `internal/context/manager.go` imports `internal/tool` (for `TodoFilePath`)
- **Description**: The context manager (a low-level package) imports the tool package (a high-level package) just for `tool.TodoFilePath()`. This creates a coupling between context management and tool infrastructure. If tool is modified, context must be recompiled.
- **Suggested refactoring**: Extract `TodoFilePath()` to a utility package or pass the path as a parameter instead of having context call tool.

---

## 2. Abstraction Layers

### Finding 2.1: Provider interface is well-designed (CLEAN)
- **Severity**: Low (positive finding)
- **Category**: Abstraction
- **File**: `internal/provider/provider.go`
- **Description**: The `Provider` interface has 4 methods (`Name`, `Chat`, `ChatStream`, `CountTokens`), which is appropriately sized. The supporting types (`Message`, `ContentBlock`, `StreamEvent`, `ToolDefinition`) are comprehensive but necessary. Provider package has minimal imports (only `config`, `debug`, `safego`, `util`, `version`), keeping it properly isolated.

### Finding 2.2: Tool interface is clean and well-scoped (CLEAN)
- **Severity**: Low (positive finding)
- **Category**: Abstraction
- **File**: `internal/tool/tool.go`
- **Description**: The `Tool` interface has exactly 4 methods (`Name`, `Description`, `Parameters`, `Execute`). The `Registry` pattern with thread-safe map is clean. Tool package imports are proportional to its scope.

### Finding 2.3: Agent is properly decoupled from TUI (CLEAN)
- **Severity**: Low (positive finding)
- **Category**: Abstraction
- **File**: `internal/agent/agent.go`
- **Description**: The Agent uses callback functions (`DiffConfirmFunc`, `ApprovalFunc`, `onUsage`, `onInterrupt`, `onRunResult`) instead of directly depending on TUI types. The agent package does NOT import TUI. This is a clean inversion-of-control pattern. The `RunStreamWithContent` method is well-documented and handles edge cases (empty responses, autopilot continuation, loop guards).

### Finding 2.4: Harness is properly isolated (CLEAN)
- **Severity**: Low (positive finding)
- **Category**: Abstraction
- **File**: `internal/harness/`
- **Description**: Harness imports only 6 packages: `config`, `debug`, `provider`, `safego`, `subagent`, `util`. It does NOT import `agent`, `tui`, or `tool` directly. This is a well-isolated domain package. Files are reasonably sized (largest is `release.go` at 931 LOC).

### Finding 2.5: ChatBridge abstraction is clean but underutilized
- **Severity**: Low
- **Category**: Abstraction
- **File**: `internal/webui/server.go:125`
- **Description**: `ChatBridge` interface (3 methods: `Messages`, `SendUserMessage`, `Subscribe`) is clean and properly decouples webui from agent implementation. Webui does NOT import `agent` or `tui`. However, `AgentRunner` backward-compatibility interface (lines 137+) adds unnecessary surface — consider removing in next major version.

---

## 3. Code Organization

### Finding 3.1: `internal/tui/` is massively oversized
- **Severity**: High
- **Category**: Code Organization
- **File**: `internal/tui/` (43,288 LOC, 107 files non-test)
- **Description**: The TUI package is 30% of the entire non-test codebase. It contains:
  - 20+ IM adapter panels (each 500-800 LOC) with nearly identical structure
  - Harness panel (1,781 LOC) + harness commands (1,157 LOC)
  - Inspector panel (1,262 LOC)
  - Provider panel (1,001 LOC)
  - Model update (2,366 LOC)
  - View rendering (1,732 LOC)
  - i18n (2,787 LOC)
  - Tool labels (1,635 LOC)
  
  The 20+ IM panels (QQ, TG, Discord, Slack, Feishu, WeChat, WhatsApp, Signal, IRC, Matrix, DingTalk, WeCom, Mattermost, Nostr, Twitch, PC) each duplicate similar panel key-handling and rendering logic.
- **Suggested refactoring**: 
  1. Create a generic `IMPanel` component that all IM adapters share
  2. Move IM panels to `internal/tui/im/` sub-package
  3. Extract harness UI to `internal/tui/harness/`
  4. Extract tool_labels to `internal/tui/render/` or better yet to `internal/tool/labels.go` (which already exists at 280 LOC)

### Finding 3.2: `tool_labels.go` is a monolithic switch statement
- **Severity**: Medium
- **Category**: Missing Abstraction
- **File**: `internal/tui/tool_labels.go` (1,635 LOC)
- **Description**: Three functions are enormous switch statements:
  - `describeTool()`: 595 lines — maps every tool name to display info
  - `localizedToolLabel()`: 252 lines — maps tool names to localized labels
  - `localizedToolActivity()`: 271 lines — maps tool+target to localized activity strings
  
  Adding a new tool requires editing all three functions. The same pattern exists in `internal/tool/labels.go` (280 LOC) and `internal/daemon/follow.go` — three separate label mapping systems.
- **Suggested refactoring**: Create a `ToolDescriptor` struct with fields for display name, detail pattern, activity template per language. Register descriptors alongside tool registration. Eliminate the three separate switch statements.

### Finding 3.3: `cmd/ggcode/root.go` run() function is 577 lines
- **Severity**: High
- **Category**: Code Organization
- **File**: `cmd/ggcode/root.go:362` — `run()` function (577 lines)
- **Description**: The `run()` function handles:
  - Config loading and validation
  - Provider initialization
  - Tool registry setup
  - MCP server startup
  - Memory loading
  - Agent construction with 15+ setter calls
  - Sub-agent manager setup
  - Swarm manager setup
  - A2A server startup (123 lines extracted to `startA2AServer`)
  - WebUI server setup
  - IM gateway startup
  - Knight agent initialization
  - Cron scheduler setup
  - TUI or pipe-mode launch
  
  It imports 27 internal packages. `startA2AServer()` is already extracted (good), but the remaining ~577 lines still handle too many concerns.
- **Suggested refactoring**: Extract into focused initialization functions:
  - `initAgent(cfg) *agent.Agent`
  - `initTools(cfg, agent) *tool.Registry`
  - `initMCP(cfg, registry)`
  - `initWebUI(cfg, agent, registry) *webui.Server`
  - `initIM(cfg, agent) *im.Runtime`

### Finding 3.4: TUI files mix business logic with rendering
- **Severity**: Medium
- **Category**: Separation of Concerns
- **Files**: `internal/tui/submit.go` (calls `agent.RunStreamWithContent`), `internal/tui/model_update.go` (2,366 LOC, handles harness state, permission checks, model switching)
- **Description**: `submit.go:261` directly calls `agent.RunStreamWithContent` — the TUI is not just rendering, it orchestrates the agent. `model_update.go` handles both UI updates AND business logic (harness promotion, permission escalation, session management). The `Model` struct holds references to `*agent.Agent`, `*harness.Engine`, `*knight.Runner`, etc.
- **Suggested refactoring**: Introduce a `SessionController` or `AppController` that mediates between agent/business logic and TUI rendering. The TUI Model should only hold display state, not business objects.

---

## 4. Dependency Management

### Finding 4.1: `charm.land` custom forks used consistently
- **Severity**: Low (positive finding)
- **Category**: Dependency Management
- **Description**: `charm.land/*` packages (bubbles/v2, bubbletea/v2, glamour/v2, lipgloss/v2) are consistently used across 92 files. Only 3 files reference `charmbracelet` (2 for `charmbracelet/x/term`, 1 in test data). This is intentional — the project uses custom Charm v2 forks, which is consistent.

### Finding 4.2: Build tags are consistent and well-structured
- **Severity**: Low (positive finding)
- **Category**: Dependency Management
- **Description**: Build tags are used consistently:
  - OS tags: `//go:build unix`, `//go:build darwin || linux`, `//go:build windows`
  - Test isolation: `//go:build integration_local` for local integration tests, `//go:build integration` for CI integration tests, `//go:build manual` for interactive tests
  - No `goolm` build tag found in production code (only in test data string)
  
  The separation of `integration_local` vs `integration` is thoughtful.

### Finding 4.3: `internal/cost/` is dead code
- **Severity**: Medium
- **Category**: Dead Code
- **File**: `internal/cost/` (280 LOC, 4 files + 2 test files)
- **Description**: No package imports `internal/cost`. The `TokenUsage` type was duplicated here to avoid circular deps, but the package was never integrated. The tracker, pricing, and manager are all unused.
- **Suggested refactoring**: Delete the entire `internal/cost/` package or integrate it into the cost tracking system.

### Finding 4.4: `google/uuid` used only 2 times — could use crypto/rand
- **Severity**: Low
- **Category**: Dependency Reduction
- **Files**: `internal/a2a/examples/rest-api/middleware/middleware.go`, `internal/im/instance_detect.go`
- **Description**: UUID is only used in 2 files, one of which is an example. Could be replaced with `crypto/rand` hex encoding for ~10 lines of code.
- **Suggested refactoring**: Replace with `crypto/rand` hex string or remove the dependency if the example is not critical.

---

## 5. Maintainability Metrics

### Finding 5.1: 25 files exceed 1000 LOC
- **Severity**: Medium
- **Category**: File Size
- **Description**: 25 non-test files exceed 1000 LOC. The top offenders:
  - `internal/tui/i18n.go`: 2,787 LOC (translation tables)
  - `internal/tui/model_update.go`: 2,366 LOC (UI update handler)
  - `internal/config/config.go`: 2,140 LOC (config loading)
  - `internal/im/runtime.go`: 1,880 LOC (IM runtime)
  - `internal/knight/scheduler.go`: 1,878 LOC (knight scheduler)
  - `internal/webui/server.go`: 1,788 LOC (HTTP server)
  
- **Suggested refactoring**: The i18n file is inherently large (translation data). The others should be split: config into loader/validator/types, model_update into per-feature update handlers.

### Finding 5.2: Massive code duplication — `firstNonEmpty` copied 32 times
- **Severity**: High
- **Category**: Code Duplication
- **Description**: `firstNonEmpty(values ...string) string` is duplicated **32 times** across the codebase. Each copy has a slightly different name (`firstNonEmpty`, `firstNonEmptyDingtalk`, `firstNonEmptyWA`, `firstNonEmptyIRC`, etc.) but identical logic. This is a clear case of copy-paste programming.
  
  Similarly, `truncateStr(s string, maxLen int) string` is duplicated **15+ times** across packages: `agent`, `tui`, `a2a`, `knight`, `harness`, `daemon`, `im`, `webui`, `permission`, `tool`.
  
  `prettifyToolName(name string) string` is duplicated 4 times: `tui/tool_labels.go`, `daemon/follow.go`, `im/tool_status.go`, `tool/labels.go`.
- **Suggested refactoring**: Move `firstNonEmpty`, `truncateStr`, `prettifyToolName`, `shortenPath` to `internal/util/` and import from there. The util package already has `Truncate()` but it's not used consistently.

### Finding 5.3: Functions exceeding 200 lines
- **Severity**: Medium
- **Category**: Function Complexity
- **Top offenders**:
  - `describeTool()` in `tool_labels.go`: 595 lines
  - `localizedToolLabel()` in `tool_labels.go`: 252 lines
  - `localizedToolActivity()` in `tool_labels.go`: 271 lines
  - `run()` in `root.go`: 577 lines
  - `REPL.Run()` in `repl.go`: 247 lines
  - `runAgentWithContent()` in `submit.go`: 261 lines
  - `handleChatWS()` in `server.go`: 157 lines
  - `renderStreamPanelRight()` in `stream_panel.go`: 142 lines
  - `inspectorText()` in `stream_panel.go`: 357 lines
- **Suggested refactoring**: Break the 500+ line functions into focused sub-functions. The describeTool switch statement should be data-driven (see Finding 3.2).

### Finding 5.4: Hardcoded magic numbers in context management
- **Severity**: Medium
- **Category**: Magic Numbers
- **Files**: 
  - `internal/agent/agent.go:87` — `128000` (default context window size, though also defined as `defaultContextWindow` in `config/context_window.go`)
  - `internal/context/manager.go:807` — `8192`, `512` (compaction thresholds)
  - `internal/context/manager.go:827` — `4096` (safety floor)
  - Multiple TUI files: `250ms` sleep delays repeated across IM panels
  - `internal/webui/server.go`: WebSocket write buffer `256` (magic channel size)
  - `internal/stream/manager.go:337`: channel buffer `64`
- **Suggested refactoring**: Define named constants for context management thresholds. Create shared IM panel timing constants. Name WebSocket buffer sizes.

### Finding 5.5: Comment quality is generally good
- **Severity**: Low (positive finding)
- **Category**: Documentation
- **Description**: Key functions have doc comments (`RunStreamWithContent`, `ChatBridge`, `Provider`, `Tool`). Complex logic like autopilot loop guards and empty response detection have inline comments explaining the "why". The agent loop has debug logging at key decision points.

---

## 6. Configuration Complexity

### Finding 6.1: Config schema is large but well-structured
- **Severity**: Medium
- **Category**: Configuration
- **File**: `internal/config/config.go` (2,140 LOC), `ggcode.example.yaml` (628 lines)
- **Description**: The `Config` struct has 30 fields and 22 nested struct types. This is complex but reflects the product's breadth (multi-vendor LLM, IM adapters, A2A auth, harness, knight, MCP). The validation function (`Validate()`) and legacy migration functions are present and documented.
- **Suggested refactoring**: Consider splitting `config.go` into `config_types.go`, `config_loader.go`, `config_validate.go`, and `config_migrate.go`.

### Finding 6.2: Deprecated config handling is thorough
- **Severity**: Low (positive finding)
- **Category**: Configuration
- **File**: `internal/config/config.go:821`, `internal/config/instance.go:507`
- **Description**: Legacy `provider/providers` keys are explicitly rejected with clear error messages. `migrateLegacyMaxIterations()` handles old format. `.ggcode/a2a.yaml` migration to instance config is handled with backward compatibility. 32 references to migration/legacy handling show careful attention to backward compatibility.

### Finding 6.3: Instance config override pattern is clean
- **Severity**: Low (positive finding)
- **Category**: Configuration
- **File**: `internal/config/instance.go`, `internal/config/a2a_override.go`
- **Description**: `.ggcode/a2a.yaml` provides instance-level A2A config override. The pattern is well-implemented with legacy migration path. The `LoadWithInstance()` function handles the cascade properly.

---

## Architecture Diagram (Import Layers)

```
Layer 0 (no internal deps):
  cost, cron, diff, extract, image, restart, safego, task, util, version

Layer 1 (depends only on Layer 0):
  auth (debug, safego)
  checkpoint (util)
  commands (config)
  debug (safego)
  hooks (util)
  markdown (none)
  memory (config)
  stream (debug)

Layer 2 (depends on Layer 0-1):
  context (debug, provider, tool)  ← concern: tool dependency
  permission (config, debug, util)
  provider (config, debug, safego, util, version)
  session (config, provider)

Layer 3 (depends on Layer 0-2):
  agent (checkpoint, context, debug, diff, hooks, memory, permission, provider, safego, tool, util)
  harness (config, debug, provider, safego, subagent, util)
  mcp (auth, config, debug, safego, tool)
  plugin (config, debug, mcp, safego, tool)
  subagent (config, debug, provider, util)
  swarm (config, debug, provider, safego, task, util)
  update (config, install, version)

Layer 4 (depends on Layer 0-3):
  a2a (agent, auth, config, debug, permission, provider, safego, tool)
  acp (agent, auth, checkpoint, config, debug, mcp, memory, permission, provider, safego, tool, version)
  chat (markdown)
  daemon (chat, config, debug, util)
  im (agent, config, daemon, debug, harness, image, permission, provider, safego, session, tool, util)
  knight (commands, config, debug, provider, safego, session, util)
  lsp (config, safego)
  tool (checkpoint, commands, config, cron, debug, extract, image, lsp, memory, permission, provider, safego, subagent, swarm, task, util)

Layer 5 (depends on everything):
  tui (31 of 41 internal packages)
  webui (auth, config, debug, provider, safego, session)  ← well-isolated
```

---

## Recommendations (Priority Order)

1. **[High]** Eliminate `firstNonEmpty` duplication (32 copies) → `internal/util/`
2. **[High]** Eliminate `truncateStr` duplication (15+ copies) → `internal/util/`
3. **[High]** Split `cmd/ggcode/root.go` `run()` into focused init functions
4. **[High]** Split `internal/tui/` into sub-packages (panels, harness, knight, render)
5. **[Medium]** Delete dead `internal/cost/` package or integrate it
6. **[Medium]** Make tool label rendering data-driven instead of switch statements
7. **[Medium]** Extract `TodoFilePath` from `tool` to break `context→tool` coupling
8. **[Medium]** Split `internal/config/config.go` (2140 LOC) into focused files
9. **[Medium]** Define named constants for context management magic numbers
10. **[Medium]** Create generic IM panel component to reduce duplication across 20+ adapters
