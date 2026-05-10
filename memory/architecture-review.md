# Architecture & Design Review — ggcode

**Reviewer:** arch-reviewer  
**Date:** 2025-07-14  
**Scope:** Full architecture review of `github.com/topcheer/ggcode`  
**Codebase:** ~138k LOC non-test, ~67k LOC test across 33+ internal packages  

---

## Executive Summary

The ggcode codebase demonstrates **strong architectural foundations** with clean interface segregation, zero circular dependencies, and consistent Go idioms throughout. The dependency graph is a proper DAG with well-defined layering. Key risks center on **one god-package (TUI)**, **one high-coupling hub (tool)**, and a **duplicated type** used to prevent circular imports. These are manageable but merit attention as the codebase scales.

**Overall Architecture Grade: B+**

---

## 1. Package Dependency Analysis

### 1.1 Dependency Graph Topology

The internal package graph is a **clean directed acyclic graph (DAG)** — zero cycles detected at any depth. This is excellent and indicates disciplined dependency management.

**Layer hierarchy (simplified):**

```
cmd/ggcode          → tui, daemon, agent, webui, harness, ...
tui (33 deps)       → all internal packages (integration layer)
tool (16 deps)      → checkpoint, commands, config, cron, extract, image, lsp, memory, permission, provider, subagent, swarm, task, util, debug, safego
acp (12 deps)       → agent, auth, checkpoint, config, debug, mcp, memory, permission, provider, safego, tool, version
im (12 deps)        → agent, config, daemon, debug, harness, image, permission, provider, safego, session, tool, util
agent (11 deps)     → checkpoint, context, debug, diff, hooks, memory, permission, provider, safego, tool, util
a2a (8 deps)        → agent, auth, config, debug, permission, provider, safego, tool
knight (7 deps)     → commands, config, debug, provider, safego, session, util
harness (6 deps)    → config, debug, provider, safego, subagent, util
webui (6 deps)      → auth, config, debug, provider, safego, session
config (5 deps)     → auth, debug, hooks, stream, util
```

**Leaf packages** (0–1 internal deps): `util`, `safego`, `version`, `checkpoint`, `hooks`, `markdown`, `chat`, `stream`, `memory`, `install`, `extract`

### 1.2 Fan-In (Most Depended Upon)

| Package | Depended on by | Role |
|---------|---------------|------|
| `config` | 21 packages | Global config singleton — **high coupling** |
| `debug` | 20 packages | Ubiquitous logging — acceptable utility |
| `safego` | 16 packages | Goroutine safety wrapper — acceptable utility |
| `provider` | 13 packages | LLM provider interface — core contract |
| `util` | 12 packages | Shell/text utilities — acceptable |
| `tool` | 8 packages | Tool registry and definitions — core contract |
| `permission` | 6 packages | Permission policy — security boundary |
| `auth` | 6 packages | Authentication subsystem |

### 1.3 Severity Ratings

| # | Finding | Severity | Details |
|---|---------|----------|---------|
| A1 | `config` package as a 21-way hub | 🟡 **Medium** | `config` is imported by nearly every package. Any config struct change triggers widespread recompilation. The package itself imports `auth`, `hooks`, `stream`, `util` — making it mid-layer, not a true leaf. |
| A2 | `tool` package fan-out of 16 | 🟡 **Medium** | The `tool` package imports 16 other internal packages (checkpoint, commands, config, cron, extract, image, lsp, memory, permission, provider, subagent, swarm, task, util, debug, safego). It's both a contract definition (`Tool` interface) and a concrete implementation hub. |
| A3 | `tui` package imports all 33 packages | 🔴 **High** | The TUI package is a **de facto integration layer** that wires everything together. It has zero reusability outside the TUI context and any internal package change triggers TUI recompilation. This is the biggest architectural risk. |

---

## 2. Circular Dependency Risk Assessment

### 2.1 Current State: ✅ Zero Cycles

The full transitive closure analysis confirms **no circular dependencies exist** (direct or indirect). This is a significant achievement for a codebase of this size.

### 2.2 Near-Miss Risk Areas

| Risk Area | Packages Involved | Current Guard | Risk |
|-----------|------------------|---------------|------|
| **TokenUsage duplication** | `provider` ↔ `cost` | `cost/types.go` duplicates the struct | 🟡 **Medium** — Drift risk if one is updated without the other |
| **config → auth → config** | `config` imports `auth`; `auth` does NOT import `config` | `auth` takes params, not config object | ✅ Clean |
| **tool → swarm → task → tool** | `tool` imports `swarm`; `swarm` imports `task`; neither imports `tool` back | Clean boundary | ✅ Clean |
| **im → daemon ↔ im** | `im` imports `daemon`; `daemon` does NOT import `im` | `daemon` uses `chat` abstraction | ✅ Clean |
| **agent ↔ context → tool → subagent → provider** | Chain is one-directional | `agent` is never imported by its transitive deps except via `tui`/`a2a`/`acp` | ✅ Clean |

### 2.3 Future Risk: Adding Features

The most likely cycle-introduction scenarios:
1. **If `provider` ever needs tool info** → would create `provider → tool → provider` cycle. Currently safe because provider is a pure LLM abstraction.
2. **If `session` grows agent awareness** → `session → agent → provider → ... → session` risk. Currently `session` only imports `config` and `provider` (for types), never `agent`.
3. **If `config` needs more behavioral deps** → already at 5 imports; adding more deepens its coupling.

---

## 3. Interface Design Review

### 3.1 Core Interfaces — Well Designed

| Interface | Package | Methods | Assessment |
|-----------|---------|---------|------------|
| `Provider` | `provider` | `Name()`, `Chat()`, `ChatStream()`, `CountTokens()` | ✅ **Excellent** — Small, focused, satisfies ISP |
| `Tool` | `tool` | `Name()`, `Description()`, `Parameters()`, `Execute()` | ✅ **Excellent** — Clean contract |
| `PermissionPolicy` | `permission` | `Check()`, `Mode()`, `IsDangerous()`, `AllowedPath()`, `AllowedPathForTool()`, `SetOverride()` | 🟡 **Medium** — 6 methods; borderline but justified |
| `Plugin` | `plugin` | `Name()`, `Tools()`, `Init()` | ✅ **Excellent** — Minimal |
| `Store` | `session` | `Save()`, `Load()`, `List()`, `Delete()`, `ExportMarkdown()`, `CleanupOlderThan()`, `AppendCheckpoint()` | 🟡 **Medium** — 7 methods; could split read/write |
| `ChatBridge` | `webui` | `Messages()`, `SendUserMessage()`, `Subscribe()` | ✅ **Excellent** — Clean decoupling |
| `ContextManager` | `context` | 8 methods | 🟡 **Medium** — Could benefit from read/write split |
| `Sink` (IM) | `im` | `Name()`, `Send()` | ✅ **Excellent** — Minimal adapter contract |
| `Bridge` (IM) | `im` | `SubmitInboundMessage()` | ✅ **Excellent** — Single method |

### 3.2 Interface Segregation Patterns

The codebase uses **local interface narrowing** effectively — packages define small private interfaces for the subset of behavior they need:

- `agent/agent.go`: `providerAwareContextManager`, `usageAwareContextManager`, `todoPathAwareContextManager`, `modeAwarePolicy`
- `agent/agent_compact.go`: `microcompacter`, `promptBudgeter`, `oldestGroupTruncater`
- `agent/agent_precompact.go`: `snapshotCompactManager`
- `tool/mcp_runtime.go`: `MCPRuntime`
- `tool/config_tool.go`: `ConfigAccess`
- `tool/plan_mode_tools.go`: `ModeSwitcher`
- `tool/skill.go`: `SkillLookup`, `skillUsageRecorder`
- `tool/task_tools.go`: `BackgroundTaskProvider`
- `webui/server.go`: `ChatBridge`, `AgentRunner`
- `webui/tui_bridge.go`: `TUIAgent`, `WebchatMessageSender`

**This is a Go best practice and is applied consistently.** ✅

### 3.3 Interface Design Issues

| # | Finding | Severity | Details |
|---|---------|------|---------|
| I1 | `TokenUsage` struct duplication | 🟡 **Medium** | `provider.TokenUsage` and `cost.TokenUsage` are identical structs. The `cost` package exists solely to avoid `provider → cost` cycles. Session store uses `CostJSON []byte` to avoid importing `cost`. This works but creates drift risk. |
| I2 | `Agent` struct uses private type assertions | 🟢 **Low** | `agent.go` uses `a.contextManager.(providerAwareContextManager)` pattern. This is idiomatic Go but fragile — if `ContextManager` implementation changes, errors surface at runtime. |
| I3 | `NewProvider()` is a factory function, not an interface method | 🟢 **Low** | `registry.go` uses a `switch` statement for protocol dispatch. Adding a new protocol requires modifying this file. Acceptable for 4 protocols; would benefit from a registry pattern at 8+. |

---

## 4. Module Boundary Assessment

### 4.1 Boundary Quality by Package

| Package | Boundary Quality | Notes |
|---------|-----------------|-------|
| `provider` | ✅ **Strong** | Clean interface + 4 concrete adapters. Types are self-contained. |
| `tool` | 🟡 **Mixed** | Interface is clean, but `tool` package imports 16 other packages — it's more of an "aggregation hub" than a boundary. Tool implementations live here alongside the interface. |
| `agent` | ✅ **Strong** | Well-scoped agent loop. Uses callback injection (DiffConfirmFunc, ApprovalFunc, etc.) to avoid importing TUI. |
| `permission` | ✅ **Strong** | Pure policy logic, minimal deps (config, debug, util). |
| `session` | ✅ **Strong** | Clean Store interface + JSONL implementation. Good separation. |
| `config` | 🟡 **Mixed** | Imports `auth`, `hooks`, `stream`, `util` — it's not a pure data package. Config loading triggers auth initialization. |
| `harness` | ✅ **Strong** | Self-contained workflow engine with only 6 deps. Clean separation from TUI. |
| `im` | ✅ **Strong** | Well-designed adapter pattern with `Sink` interface. 17 adapters following consistent structure. |
| `webui` | ✅ **Strong** | `ChatBridge` interface cleanly decouples from both TUI and daemon. |
| `a2a` | ✅ **Strong** | Self-contained protocol implementation with clean client/server split. |
| `knight` | ✅ **Strong** | Background agent with focused deps. |
| `tui` | 🔴 **Weak** | 107 files, 43k LOC, imports everything. Not a module boundary — it's the integration point. |

### 4.2 Cross-Cutting Concerns

| Concern | Mechanism | Assessment |
|---------|-----------|------------|
| **Logging** | `debug` package (imported by 20 packages) | ✅ Lightweight, no coupling |
| **Goroutine safety** | `safego` package (imported by 16 packages) | ✅ Clean utility |
| **Error handling** | `fmt.Errorf("...: %w", err)` wrapping | ✅ Consistent Go idiom |
| **Concurrency** | `sync.RWMutex` in `Agent`, `Registry`, `session.JSONLStore` | ✅ Proper lock discipline |
| **Platform-specific code** | Build tags (`//go:build unix`, `//go:build !unix`) | ✅ Cleanly separated |
| **i18n** | `tui/i18n.go` with en/zh-CN catalogs | ✅ Centralized, consistent |

---

## 5. Code Organization Consistency

### 5.1 Positive Patterns

1. **Consistent import alias for `context`**: `ctxpkg` is used throughout to avoid shadowing stdlib `context`. Documented in `GGCODE.md`.
2. **Consistent file naming**: `agent_*.go` split files in `agent/`, `*_adapter.go` in `im/`, `*_test.go` alongside sources.
3. **Consistent interface placement**: Interfaces defined in the consuming package when they're narrow (e.g., `ChatBridge` in `webui`), or in the providing package for core contracts (e.g., `Provider` in `provider`, `Tool` in `tool`).
4. **Platform-specific code**: Properly isolated with build tags.
5. **Test tagging**: Integration tests use `//go:build integration` consistently.

### 5.2 Organization Issues

| # | Finding | Severity | Details |
|---|---------|------|---------|
| O1 | **TUI god-package** (43k LOC, 107 files, 33 imports) | 🔴 **High** | The TUI package is the single biggest architectural liability. It's an unmaintainable monolith that contains: model definition, views, all panel implementations, slash commands, harness commands, keyboard handling, IM panels, i18n, file browser, and more. A change to any internal package recompiles the entire TUI. |
| O2 | **`cmd/ggcode/root.go`** at 1414 lines / 32 functions | 🟡 **Medium** | This file handles CLI setup, tool registration, A2A setup, MCP bridge setup, daemon launch, and more. It's a "main on steroids" that should be decomposed. |
| O3 | **IM package at 42k LOC** | 🟡 **Medium** | The IM package is very large, but this is justified by having 17+ adapter implementations following a consistent pattern. The core runtime/bridge/types are well-separated from adapters. The risk is manageable. |
| O4 | **Tool registration split** across 3 locations | 🟡 **Medium** | Built-in tools in `tool/builtin.go`, agent tools in `tui/repl.go`, MCP/skill/save_memory in `cmd/ggcode/root.go`. This dispersal makes it hard to see the complete tool inventory in one place. |
| O5 | **Provider-specific types leak through boundaries** | 🟢 **Low** | `provider.ContentBlock` is a "god struct" with 12+ fields (text, image, tool_use, tool_result, reasoning, thinking). It's used across agent, session, webui, and tui. Adding a new content type requires modifying the provider package. |

---

## 6. Architectural Layering

```
┌─────────────────────────────────────────────────────┐
│  cmd/ggcode (CLI entry, wiring)                      │
├─────────────────────────────────────────────────────┤
│  tui │ daemon │ webui │ im     (Integration Layer)   │
├─────────────────────────────────────────────────────┤
│  agent │ harness │ knight │ a2a  (Orchestration)     │
├─────────────────────────────────────────────────────┤
│  tool │ subagent │ swarm │ mcp    (Capability)       │
├─────────────────────────────────────────────────────┤
│  provider │ permission │ session  (Core Contracts)   │
├─────────────────────────────────────────────────────┤
│  config │ auth │ context │ memory   (Infrastructure) │
├─────────────────────────────────────────────────────┤
│  debug │ safego │ util │ version   (Utilities)       │
└─────────────────────────────────────────────────────┘
```

**This layering is mostly clean.** The main violation is `tool` importing `subagent` and `swarm` (cross-layer dependency from Capability → Orchestration).

---

## 7. Summary of Findings by Severity

### 🔴 High Severity

| ID | Finding | Impact | Recommendation |
|----|---------|--------|----------------|
| A3 | **TUI god-package** (43k LOC, 33 deps) | Maintainability, recompilation time, testability | Decompose into `tui/core`, `tui/panels`, `tui/commands`, `tui/views`. Extract panels into sub-packages by domain (IM panels, harness panel, etc.). |
| O1 | **TUI is integration layer, not a module** | Any change propagates to TUI; circular dependency risk if TUI types leak back | Establish a `tui/integration` wiring layer and move domain-specific panel logic closer to its source package. |

### 🟡 Medium Severity

| ID | Finding | Impact | Recommendation |
|----|---------|--------|----------------|
| A1 | **`config` as 21-way hub** | Widespread recompilation on config changes | Split `config` into `config/core` (pure data loading) and `config/setup` (initialization that imports auth, hooks, etc.). |
| A2 | **`tool` package 16-dep fan-out** | `tool` is both interface and implementation hub | Separate `tool/contract` (Tool interface, Result, Registry) from `tool/impl` (concrete tool implementations). |
| I1 | **TokenUsage duplication** (`provider` vs `cost`) | Drift risk between identical structs | Define `TokenUsage` once in `provider` (already done) and remove `cost/types.go`. Have `cost` import `provider` or use a shared `types` leaf package. |
| O2 | **root.go at 1414 LOC / 32 funcs** | Hard to navigate, mixes concerns | Extract into `cmd/ggcode/setup.go`, `cmd/ggcode/a2a.go`, `cmd/ggcode/daemon.go`, etc. |
| O3 | **IM package at 42k LOC** | Navigability | Already well-structured internally. Consider `im/adapters/` sub-package for the 17 adapter files. |
| O4 | **Tool registration split across 3 locations** | Hard to audit tool inventory | Consolidate registration into a single `RegisterAllTools()` function in `tool/registry.go` or a dedicated `tool/setup.go`. |

### 🟢 Low Severity / Informational

| ID | Finding | Impact | Recommendation |
|----|---------|--------|----------------|
| I2 | Private type assertions in Agent | Runtime-only error surface | Acceptable Go pattern; document expected interface compliance |
| I3 | Provider factory switch statement | Adding protocol requires code change | Acceptable for 4 protocols; add registry if more |
| O5 | ContentBlock "god struct" (12+ fields) | Adding content types touches provider | Could use sum-type pattern with interfaces, but current approach is pragmatic |
| — | `i18n.go` at 2783 lines | Mostly data (translation tables) | Acceptable for i18n; consider code generation from translation files |

---

## 8. Strengths

1. **Zero circular dependencies** — exemplary for a 138k LOC Go project
2. **Clean core interfaces** — `Provider`, `Tool`, `Plugin`, `Sink`, `ChatBridge` are textbook Go interfaces
3. **Consistent use of interface narrowing** — private interfaces in consuming packages follow ISP perfectly
4. **Good callback/function injection** — `DiffConfirmFunc`, `ApprovalFunc` avoid hard dependencies on UI packages
5. **Platform-specific code properly isolated** — build tags used correctly
6. **Well-designed adapter patterns** — IM adapters (17), provider adapters (4), webui bridges (2) all follow consistent patterns
7. **Proper concurrency discipline** — `sync.RWMutex` with consistent lock ordering
8. **Session storage** — JSONL with atomic writes, index, and auto-repair is well-engineered

---

## 9. Recommendations Priority Order

1. **[P0] Decompose TUI package** — Extract into sub-packages. This is the highest-impact architectural improvement.
2. **[P1] Consolidate tool registration** — Single `RegisterAll()` function, not scattered across 3 files.
3. **[P1] Remove TokenUsage duplication** — Keep one canonical definition.
4. **[P2] Decompose root.go** — Split into domain-focused files.
5. **[P2] Split config package** — Separate pure data loading from initialization.
6. **[P3] Extract tool contract from implementation** — Separate interface from concrete tools.

---

*End of Architecture & Design Review*
