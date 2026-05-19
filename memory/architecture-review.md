# ggcode Architecture Review

Reviewer: Senior Go Architect
Date: 2025-07-13
Codebase: ~143k LOC (non-test) across 43 internal packages

---

## Executive Summary

ggcode is a feature-rich terminal-based AI coding agent with multi-LLM provider support,
17+ IM platform adapters, an embedded WebUI, a harness-engineering workflow engine,
sub-agent/swarm coordination, A2A protocol, and a Knight background agent. The codebase
demonstrates strong Go idioms in many areas (interfaces, concurrency, error handling)
but suffers from two systemic architectural issues: (1) a "God package" problem where
`internal/tui` (44k LOC) and `internal/im` (24k LOC) are monolithic, and (2) a
"God orchestrator" problem where `cmd/ggcode/root.go` and `internal/tui/repl.go`
wire together 30+ packages with no layered abstraction boundary.

---

## Dimension Scores

| Dimension | Score | Summary |
|-----------|-------|---------|
| 1. Package Structure | 6/10 | Core packages well-separated; tui/im are monoliths |
| 2. Layered Design | 5/10 | cmd layer is flat; no dependency inversion |
| 3. Interface Design | 8/10 | Provider/Tool/Plugin/Sink/Bridge are idiomatic Go |
| 4. Modularity/Extensibility | 8/10 | Provider/IM/Tool extension points are clean |
| 5. Concurrency Model | 7/10 | Good context propagation; some goroutine leaks possible |
| 6. Dependency Management | 7/10 | Fyne desktop coupling is the main concern |

---

## 1. Package Structure — 6/10

### Positive
- **43 packages in `internal/`** is a reasonable decomposition for a project this size.
- Core domain packages (`provider`, `tool`, `agent`, `permission`, `config`, `session`)
  have clear single responsibilities.
- Utility packages (`debug`, `util`, `diff`, `version`, `safego`) are properly isolated.
- `internal/cost/types.go` deliberately duplicates `TokenUsage` to prevent circular imports
  with `internal/provider` — a pragmatic and documented tradeoff.

### Issues

**P1: `internal/tui` is a 44k LOC monolith (47+ files)**
- `internal/tui/model.go` alone imports 32 other internal packages.
- This is the largest package in the codebase and acts as the integration hub.
- Sub-topics like panels, views, i18n, slash commands, harness UI, IM UI, skills UI,
  file browser, preview, and inspector could each be separate sub-packages.

**P2: `internal/im` is a 24k LOC monolith (80+ files)**
- Contains 17 adapter implementations + runtime + binding store + pairing + daemon bridge
  + emitter + formatting all in one flat package.
- Adapter implementations (`qq_adapter.go`, `tg_adapter.go`, etc.) are individually
  well-structured but crammed into the parent package.

**P3: Adapter factory is a hardcoded switch statement**
- `internal/im/adapters.go:105-222` — a 117-line switch/case across 17 platforms.
- Adding a new IM platform requires editing this central file, violating OCP.

### Recommendations
1. Split `internal/tui` into `internal/tui/core`, `internal/tui/panels`, `internal/tui/views`,
   `internal/tui/i18n`, `internal/tui/harness`, etc.
2. Split `internal/im` into `internal/im/core` (Manager, types, Sink interface),
   `internal/im/adapter/{qq,tg,discord,...}` (each adapter as sub-package),
   `internal/im/bridge` (DaemonBridge), `internal/im/pairing`.
3. Introduce an adapter registry pattern to replace the switch/case factory.

---

## 2. Layered Design — 5/10

### Positive
- `internal/provider` provides clean abstraction over LLM backends.
- `internal/agent` properly depends on `provider` and `tool` interfaces.
- `internal/webui` uses `ChatBridge` interface to decouple from agent implementation.

### Issues

**P1: `cmd/ggcode/root.go` (1424 lines) is a God function**
- Imports 30 internal packages directly.
- The `RunE` function handles config loading, pipe mode, TUI setup, daemon setup,
  provider construction, session loading, MCP initialization, IM setup, webui setup,
  harness setup, knight setup, sub-agent manager setup, swarm manager setup, and
  signal handling — all in one function.
- This makes testing and understanding the startup flow extremely difficult.

**P2: `internal/tui/repl.go` is a secondary orchestrator**
- Imports 20+ internal packages (lines 3-37).
- The `REPL` struct acts as a "God object" holding references to agent, session store,
  MCP manager, IM manager, command manager, knight, etc.
- This is fundamentally the same pattern as root.go — just at a different level.

**P3: No dependency injection container or app context**
- Dependencies are wired manually via constructor functions and setter methods.
- `root.go` creates a `*webui.Server` and then calls `SetMCPStatusFn`, `SetIMStatusFn`,
  `SetIMActionFn`, `SetRestartFn`, `SetA2ADiscoverFn`, `SetKnightStatusFn`,
  `SetKnightActionFn`, `SetKnightSkillContentFn`, `SetSessionStore`, `SetAgent`,
  `SetChatBridge`, `SetSaveScope` — 12 setter calls for one object.
- This "setter injection" pattern is fragile and hides initialization ordering requirements.

### Recommendations
1. Introduce an `App` struct in `internal/app` that holds all subsystem references and
   provides a clear `Initialize() → Run() → Shutdown()` lifecycle.
2. Extract startup orchestration from `root.go` into a dedicated `internal/bootstrap`
   package with small, testable functions.
3. Consider a lightweight DI approach (constructor injection) over setter injection.

---

## 3. Interface Design — 8/10

### Positive

**`provider.Provider`** (provider.go:123-138) — Excellent:
- 4 methods: `Name`, `Chat`, `ChatStream`, `CountTokens`
- Small, focused interface. Each method has a clear contract.
- Protocol adapters (OpenAI, Anthropic, Gemini, Copilot) all implement it cleanly.

**`tool.Tool`** (tool/tool.go:40-53) — Excellent:
- 4 methods: `Name`, `Description`, `Parameters`, `Execute`
- Follows the "accept interfaces, return structs" Go idiom.
- The `Cloner` optional interface pattern is well-designed for concurrent sub-agents.

**`plugin.Plugin`** (plugin/plugin.go:15-25) — Good:
- 3 methods: `Name`, `Tools`, `Init`
- Clean separation of plugin lifecycle from tool registration.

**`im.Sink`** (im/types.go:256-259) — Good:
- 2 methods: `Name`, `Send`
- Plus optional interfaces: `TypingIndicator`, `InteractiveSender`, `Closer`
- This is the Go "interface segregation" pattern done correctly.

**`webui.ChatBridge`** (webui/server.go:117-127) — Good:
- 3 methods: `Messages`, `SendUserMessage`, `Subscribe`
- Clean decoupling of WebUI from agent implementation.
- `TUIChatBridge` and `DaemonBridge` both implement it.

**`im.Bridge`** (im/types.go:252-254) — Good but minimal:
- Single method: `SubmitInboundMessage`
- Clean inbound abstraction.

### Issues

**P2: `swarm.AgentRunner` re-declares a subset of `agent.Agent` methods**
- `internal/swarm/manager.go:21-23` defines `AgentRunner` with just `RunStream`.
- But `webui/server.go:131-134` also defines `AgentRunner` with different methods.
- Two packages defining the same-named interface with different method sets is confusing.

**P3: `swarm.ToolBuilder` uses `interface{}` return type**
- `internal/swarm/manager.go:25`: `ToolBuilder func(allowedTools []string) interface{}`
- Loses type safety — the factory returns an untyped tool set.

**P3: `swarm.AgentFactory` uses `interface{}` for tools parameter**
- `internal/swarm/manager.go:17`: `AgentFactory func(... tools interface{}, ...) AgentRunner`
- Same issue — runtime type assertion required.

### Recommendations
1. Unify the `AgentRunner` interface definitions into one canonical location (e.g., `internal/agent/runner.go`).
2. Replace `interface{}` in swarm with `*tool.Registry`.

---

## 4. Modularity & Extensibility — 8/10

### Positive

**Provider extensibility** — Excellent:
- `internal/provider/registry.go` uses a simple factory function.
- Adding a new provider requires only: (1) implement `Provider` interface, (2) add a case to `NewProvider`.
- The adaptive cap (`internal/provider/adaptive_cap.go`) is a nice cross-cutting feature.

**Tool extensibility** — Excellent:
- `tool.Registry` provides thread-safe registration with `Register`, `Get`, `Unregister`, `Clone`.
- MCP tools are dynamically loaded and adapted via `internal/plugin/mcp_loader.go`.
- External command-based plugins via `plugin.CommandTool`.
- The `Cloner` interface for stateful tools is well-designed for sub-agent isolation.

**IM adapter extensibility** — Good (with caveats):
- The `Sink` interface is minimal and clean.
- Each adapter is self-contained (its own file with constructor).
- Optional interfaces (`InteractiveSender`, `TypingIndicator`, `Closer`) allow progressive enhancement.

**WebUI extensibility** — Good:
- REST API endpoints follow a consistent pattern.
- The `ChatBridge` interface cleanly abstracts TUI vs daemon mode.

### Issues

**P2: IM adapter factory is not pluggable**
- The 117-line switch/case in `adapters.go` must be edited for every new platform.
- No `RegisterAdapter(platform, factory)` pattern exists.

**P3: Harness storage is hardcoded to JSON files**
- No `EventStore` interface — direct file I/O in harness logic.
- Would benefit from the same interface pattern used by `session.Store`.

### Recommendations
1. Introduce `im.AdapterFactory` interface and a global registry.
2. Extract harness storage behind an `EventStore` interface.

---

## 5. Concurrency Model — 7/10

### Positive

**safego package** — Excellent:
- `internal/safego/safego.go` provides panic recovery for all goroutines.
- The `PanicHook` mechanism allows TUI to surface errors without crashing.
- The double-recovery guard (protecting PanicHook itself) shows defensive programming.

**Context propagation** — Good:
- Agent loop properly checks `ctx.Err()` at every iteration boundary and mid-tool-execution.
- `fillCancelledToolResults` ensures protocol correctness (matching tool_use/tool_result pairs)
  even when cancelled mid-execution — a subtle and well-handled edge case.
- IM adapters get per-adapter child contexts with individual cancel functions.
- Swarm `Manager` has a `rootCtx`/`rootCancel` for clean shutdown.

**Tool registry concurrency** — Good:
- `tool.Registry` uses `sync.RWMutex` for thread-safe access.
- `Clone()` creates independent copies for sub-agents.

**WebSocket write concurrency** — Good:
- Per-connection write goroutines with buffered channels prevent concurrent write on gorilla/websocket.

### Issues

**P1: `im.DaemonBridge` has potential TOCTOU issues**
- `SendUserMessage` claims the run slot under mutex, but the agent run itself is
  started outside the lock, creating a window where the state could change.
- The code comments acknowledge this pattern exists but is acceptable for daemon mode
  since IM messages are typically serialized by the adapter.

**P2: `Agent.RunStreamWithContent` holds state across iterations without a per-run lock**
- `toolDefs` is captured once at line 404 and reused across all iterations.
- If tools are dynamically registered/unregistered mid-run, this could be stale.
- This is a minor concern since tool registration typically happens at startup.

**P3: `im.Manager.HandleInbound` has long lock hold times**
- The mutex is held for the entire inbound processing including dedup, binding update,
  and persistence writes (lines 372-465).
- High-traffic IM scenarios could see contention.

**P2: Sub-agent event buffer is a ring with unbounded memory for the ring itself**
- `internal/subagent/manager.go:35`: `maxAgentEvents = 200` caps events per agent.
- But `SubAgent.Mailbox` is an unbuffered channel, so senders could block.

### Recommendations
1. In `DaemonBridge.SendUserMessage`, hold the mutex through the agent start call to close the TOCTOU window.
2. Consider buffered channels for `SubAgent.Mailbox` with a capacity and drop policy.
3. Break `HandleInbound` into phases: validate under lock, then persist and dispatch unlocked.

---

## 6. Dependency Management — 7/10

### Positive
- Go version 1.26.1 — up to date.
- Direct dependencies are well-chosen: Cobra (CLI), Bubble Tea (TUI), gorilla/websocket,
  spf13/viper alternatives (YAML), modernc.org/sqlite.
- The project avoids heavy frameworks — most code is hand-written Go.
- `otiai10/gosseract` is only an indirect dependency (via OCR feature).
- Build tags (`goolm`) properly isolate platform-specific dependencies.

### Issues

**P1: `fyne.io/fyne/v2` is a heavy dependency for a CLI-first project**
- Fyne is a full desktop GUI framework (~2MB+ compiled).
- It's used only for the `desktop/` app (separate module, but listed in go.mod).
- This pulls in GL dependencies, JavaScript rendering, and platform-specific GUI code.
- If the desktop module is truly separate, it should have its own go.mod (it does at
  `desktop/go.mod`, but the root go.mod still lists fyne).

**P2: Two competing markdown rendering paths**
- `charm.land/glamour/v2` and `github.com/yuin/goldmark` both handle markdown.
- `github.com/alecthomas/chroma/v2` for syntax highlighting.
- There may be overlap in functionality.

**P3: 143 indirect dependencies**
- The indirect dependency count is high but not unreasonable for a project with
  IM protocol support (WhatsApp/mautrix, Matrix, Telegram, Discord, Slack, etc.).
- Each IM protocol SDK brings its own transitive deps.

### Recommendations
1. Ensure `fyne.io/fyne/v2` is only in `desktop/go.mod`, not the root `go.mod`.
2. Consider whether both glamour and goldmark are needed, or if one can replace the other.
3. Periodically audit indirect deps with `go mod why` to ensure no dead weight.

---

## Specific Issues List

| Priority | File | Line | Issue |
|----------|------|------|-------|
| P1 | `internal/tui/` | — | 44k LOC monolith package, imports 32 other internal packages |
| P1 | `internal/im/` | — | 24k LOC monolith, 80+ files, 17 adapters + runtime + bridge in one package |
| P1 | `cmd/ggcode/root.go` | 1-1424 | God function: 30+ package imports, all subsystem wiring in one RunE |
| P1 | `internal/tui/model.go` | 1-37 | Imports 32 internal packages — acts as integration hub |
| P2 | `internal/im/adapters.go` | 105-222 | 117-line hardcoded switch/case adapter factory |
| P2 | `internal/swarm/manager.go` | 17,25 | `AgentFactory` and `ToolBuilder` use `interface{}` — loses type safety |
| P2 | `internal/webui/server.go` | 130-134 | `AgentRunner` interface differs from `swarm.AgentRunner` — same name, different contract |
| P2 | `internal/tui/repl.go` | 1-37 | 20+ internal package imports — secondary orchestrator |
| P2 | `internal/im/runtime.go` | 372-465 | `HandleInbound` holds mutex during persistence writes |
| P3 | `internal/provider/registry.go` | 10-49 | Factory function switch — could use adapter registry |
| P3 | `internal/cost/types.go` | 1-10 | `TokenUsage` duplicated from `provider` to avoid circular imports |
| P3 | `go.mod` | 10 | `fyne.io/fyne/v2` in root go.mod despite separate desktop module |

---

## Improvement Recommendations (by Priority)

### Priority 1: Structural Decomposition

1. **Split `internal/tui` into sub-packages** — This is the highest-impact change.
   Target structure: `tui/core`, `tui/panels`, `tui/views`, `tui/slcommands`,
   `tui/harness`, `tui/impanel`, `tui/i18n`, `tui/preview`.

2. **Split `internal/im` into sub-packages** — `im/core` (Manager, types, Sink),
   `im/adapter/*` (each adapter), `im/bridge` (DaemonBridge), `im/pairing`, `im/format`.

3. **Extract `cmd/ggcode/root.go` startup into `internal/app`** — Create an `App`
   struct with clear lifecycle methods. This makes the codebase testable and the
   dependency graph visible.

### Priority 2: Interface Cleanup

4. **Unify `AgentRunner` interfaces** — Create `internal/agent/runner.go` with the
   canonical interface. Remove the duplicate from `internal/webui`.

5. **Replace `interface{}` in swarm** — Use `*tool.Registry` instead of untyped params.

6. **Introduce IM adapter registration** — Replace the switch/case factory with a
   `RegisterAdapter(platform string, factory AdapterFactory)` pattern.

### Priority 3: Concurrency Hardening

7. **Close TOCTOU in DaemonBridge** — Hold mutex through agent start.

8. **Buffer SubAgent.Mailbox** — Prevent sender blocking.

9. **Reduce lock hold time in HandleInbound** — Separate validation from persistence.

### Priority 4: Dependency Hygiene

10. **Remove Fyne from root go.mod** — Keep it only in `desktop/go.mod`.

11. **Consolidate markdown libraries** — Evaluate whether goldmark or glamour alone suffices.

12. **Audit indirect dependencies** — `go mod why` each indirect dep quarterly.

---

## Architecture Diagram (Dependency Flow)

```
cmd/ggcode
  ├── internal/app (RECOMMENDED: extract from root.go)
  │     ├── internal/agent
  │     │     ├── internal/provider (Provider interface)
  │     │     ├── internal/tool (Tool interface, Registry)
  │     │     ├── internal/context (ContextManager interface)
  │     │     ├── internal/permission (PermissionPolicy)
  │     │     ├── internal/checkpoint
  │     │     └── internal/hooks
  │     ├── internal/tui (44k LOC — needs decomposition)
  │     │     └── imports nearly everything
  │     ├── internal/im (24k LOC — needs decomposition)
  │     │     ├── internal/agent (via DaemonBridge)
  │     │     ├── internal/daemon
  │     │     ├── internal/harness
  │     │     └── internal/tool (via ask_user formatting)
  │     ├── internal/webui (ChatBridge interface)
  │     │     └── internal/session
  │     ├── internal/subagent
  │     ├── internal/swarm
  │     ├── internal/harness
  │     ├── internal/knight
  │     ├── internal/a2a
  │     ├── internal/mcp (via internal/plugin)
  │     └── internal/config
  ├── internal/session (Store interface)
  ├── internal/plugin (Plugin interface)
  └── internal/safego (goroutine safety)

Leaf packages (no internal deps):
  internal/debug, internal/util, internal/diff, internal/version,
  internal/safego, internal/markdown, internal/cost
```

---

## Overall Assessment

ggcode is a **well-engineered product with sound foundational patterns** that has grown
organically into architectural challenges typical of successful Go projects. The core
domain abstractions (Provider, Tool, Sink, Bridge) follow Go best practices. The main
issues are the two monolithic packages (tui, im) and the flat orchestration layer
(root.go + repl.go). These are "success problems" — the project has grown beyond its
initial structure.

The highest ROI improvement would be decomposing `internal/tui` into sub-packages and
extracting startup orchestration from `cmd/ggcode/root.go` into an `internal/app` layer.
This would improve testability, code navigation, and onboarding for new contributors.

**Overall Architecture Grade: B (7.0/10)**

Strengths: Interface design, concurrency safety, extensibility points
Weaknesses: Package granularity, orchestration complexity, God objects
