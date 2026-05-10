# Test Quality Audit Report — ggcode codebase

**Date**: 2025-05-09
**Scope**: ~69k LOC test across ~120+ test files
**Auditor**: test-auditor (TeamClaw)

---

## 1. Coverage Gaps — Critical Paths Without Tests

### HIGH-1: `internal/daemon/` — Zero Test Coverage
- **File**: `internal/daemon/follow.go` (824 LOC), `background.go` (190 LOC)
- **Severity**: **High**
- **Category**: Coverage Gap
- **Description**: The entire `daemon` package has **zero test files**. This package contains the terminal follow display (824 LOC), background forking, keyboard shortcut handling (v/q/s output mode, M/U mute, f follow toggle, r restart), and session picker. These are critical user-facing paths in daemon mode.
- **Suggested improvement**: Add unit tests for follow display rendering, keyboard shortcut routing, and output mode cycling. At minimum, test `FollowDisplay` event handling and the keyboard-to-action mapping.

### HIGH-2: `internal/agent/` — Missing Error Path Tests for Tool Execution
- **File**: `internal/agent/agent_tool.go`
- **Severity**: **High**
- **Category**: Coverage Gap
- **Description**: The agent test suite (`agent_test.go` 1657 LOC + `agent_coverage_test.go` 987 LOC) covers the main loop, compaction, and autopilot extensively. However, **tool execution error paths** are undertested:
  - No test for tool `Execute()` returning an error (tool exists but Execute fails)
  - No test for tool timeout handling
  - No test for tool returning invalid/non-JSON result
  - No test for the `hooks.PreToolUse` hook actually blocking execution (the `TestAgent_SetHookConfig` test explicitly notes it cannot verify blocking)
- **Suggested improvement**: Add `TestRunStream_ToolExecuteError`, `TestRunStream_ToolTimeout`, `TestHookBlocksExecution`.

### HIGH-3: `internal/agent/` — No Test for Run() Non-Streaming Path
- **File**: `internal/agent/agent.go`
- **Severity**: **High**
- **Category**: Coverage Gap
- **Description**: All agent tests use `RunStream` or `RunStreamWithContent`. The non-streaming `Run()` method is never tested. While it shares code with `RunStream`, it has distinct response construction logic that could diverge.
- **Suggested improvement**: Add `TestAgentRun_NonStreaming` verifying the `Chat` (non-streaming) path produces correct results.

### MEDIUM-4: `internal/agent/` — No Test for Race Between PreCompact and RunStream Start
- **File**: `internal/agent/agent_precompact.go`
- **Severity**: **Medium**
- **Category**: Coverage Gap
- **Description**: Pre-compact concurrency is tested (TestRunStreamDoesNotWaitForInFlightPreCompact, TestPreCompactAppliesCompletedSnapshotAtRunBoundary), but there is no test for the race when `StartPreCompact()` is called simultaneously with `RunStream()` starting — a potential TOCTOU issue.
- **Suggested improvement**: Add a concurrent test that calls StartPreCompact and RunStream from different goroutines.

### MEDIUM-5: `internal/tui/` — Mode Transition Coverage Incomplete
- **File**: `internal/tui/tui_test.go` (3222 LOC), `internal/tui/keyboard_interaction_test.go` (1489 LOC)
- **Severity**: **Medium**
- **Category**: Coverage Gap
- **Description**: The TUI tests (4900+ LOC across 2 files) cover many keyboard scenarios and render paths well. However:
  - No test for supervised->plan->auto->bypass->autopilot mode cycle via keyboard
  - No test for mode-specific UI changes (e.g., plan mode shows read-only badge)
  - No test for the `repl.go` submit flow when model calls `ask_user`
- **Suggested improvement**: Add `TestScenario_ModeCycle`, `TestPlanModeUI_Badge`.

### MEDIUM-6: `internal/swarm/` — Teammate Lifecycle Edge Cases
- **File**: `internal/swarm/manager_test.go`
- **Severity**: **Medium**
- **Category**: Coverage Gap
- **Description**: Team spawning, shutdown, and messaging are well tested. Missing:
  - No test for teammate timeout (TeammateTimeout config)
  - No test for what happens when teammate's agent panics mid-task
  - No test for inbox overflow (inbox full with InboxSize messages)
  - No test for team deletion while teammate is actively working
- **Suggested improvement**: Add `TestManager_TeammateTimeout`, `TestManager_TeammatePanic`, `TestManager_InboxOverflow`, `TestManager_DeleteTeamWhileWorking`.

### LOW-7: `internal/markdown/` — No Test Coverage
- **File**: `internal/markdown/markdown.go` (196 LOC)
- **Severity**: **Low**
- **Category**: Coverage Gap
- **Description**: Markdown rendering helper has no tests. Risk is moderate since it's a rendering utility.
- **Suggested improvement**: Add basic tests for markdown-to-terminal rendering.

---

## 2. Test Isolation & Reliability

### HIGH-8: Pervasive `time.Sleep` in Swarm Tests — Flaky Risk
- **Files**: `internal/swarm/idle_runner_test.go`, `internal/swarm/e2e_test.go`, `internal/swarm/collab_e2e_test.go`, `internal/swarm/manager_test.go`
- **Severity**: **High**
- **Category**: Test Isolation
- **Description**: **51 instances** of `time.Sleep` across swarm tests, with durations ranging from 20ms to 500ms. Examples:
  - `idle_runner_test.go:97` — `time.Sleep(200ms)` waiting for agent to be called
  - `idle_runner_test.go:446` — `time.Sleep(500ms)` waiting for multiple tasks
  - `manager_test.go:169` — `time.Sleep(50ms)` "let idle loops start"
  - `e2e_test.go` has 20+ sleep calls

  Under CI load, these timing assumptions will fail intermittently. The 200ms and 500ms sleeps are especially fragile — on a slow CI runner, 500ms may not be enough; on a fast machine, it wastes time.
- **Suggested improvement**: Replace `time.Sleep` with channel-based synchronization. Use `testify/assert.Eventually` or custom poll-with-timeout patterns. Example:
  ```go
  // Instead of:
  time.Sleep(200 * time.Millisecond)
  if agent.getCalls() != 1 { ... }
  
  // Use:
  assert.Eventually(t, func() bool { return agent.getCalls() == 1 }, 2*time.Second, 50*time.Millisecond)
  ```

### HIGH-9: TUI Tests Have 276 `time.Sleep` Calls
- **Files**: All `internal/tui/*_test.go` files
- **Severity**: **High**
- **Category**: Test Isolation
- **Description**: **276 instances** of `time.Sleep` across TUI tests. Many are in PTY integration tests (tagged `integration_local` so excluded from CI), but some are in the main test files:
  - `layout_test.go:2005` — `time.After(100ms)` for animation settling
  - `program_harness_test.go` — multiple 2-second timeouts
  - `im_runtime_test.go` — 2-second timeouts

  The PTY tests being `integration_local` tagged is good for CI reliability, but the non-PTY tests still have timing dependencies.
- **Suggested improvement**: Audit each `time.Sleep` in non-PTY TUI tests and replace with deterministic synchronization where possible.

### MEDIUM-10: IM Tests Depend on Timing
- **Files**: `internal/im/runtime_test.go`, `internal/im/runtime_mute_test.go`
- **Severity**: **Medium**
- **Category**: Test Isolation
- **Description**: **30 instances** of `time.Sleep` in IM tests. Many relate to waiting for adapter startup/shutdown. These are genuine concurrency tests that need some waiting, but the fixed durations (100ms, 200ms) are fragile.
- **Suggested improvement**: Use channel-based done signals or sync.WaitGroup for adapter lifecycle events.

### MEDIUM-11: Agent Tests Use `time.Sleep` in Poll Loop
- **File**: `internal/agent/agent_test.go:641-660` (`waitForPrecompactDone`)
- **Severity**: **Medium**
- **Category**: Test Isolation
- **Description**: `waitForPrecompactDone` polls with `time.Sleep(10ms)` in a loop. This is a reasonable pattern but could be replaced with a channel signal from the precompact goroutine for faster feedback.
- **Suggested improvement**: Expose a `Done()` channel from precompact state.

---

## 3. Assertion Quality

### MEDIUM-12: WebUI Coverage Test — Weak "No Panic" Assertions
- **File**: `internal/webui/coverage_test.go`
- **Severity**: **Medium**
- **Category**: Assertion Quality
- **Description**: Several tests only verify that calls don't panic or return nil, without checking side effects:
  - `TestServerSetters` — calls all setters but only checks for panics (no assertion on actual state)
  - `TestServerClose_NotStarted` — only checks `err == nil`
  - `TestServerAddr_BeforeStart` — only checks `addr == ""`
- **Suggested improvement**: Verify internal state after setter calls. For `SetChatBridge`, verify it's actually used when processing WebSocket messages.

### MEDIUM-13: Agent Coverage Test — Hook Config Not Verified
- **File**: `internal/agent/agent_coverage_test.go:220-248` (`TestAgent_SetHookConfig`)
- **Severity**: **Medium**
- **Category**: Assertion Quality
- **Description**: The test explicitly notes: "Hooks.RunPreHooks requires a real command executor; with an empty command in test env the hook may not block." It calls `executeTool` and discards the result with `_ = result`. This is effectively a "doesn't panic" test for hook integration.
- **Suggested improvement**: Create a mock hook executor that actually runs, then verify the hook blocks/allows as expected.

### LOW-14: Subagent Manager Test — Minimal Negative Path Coverage
- **File**: `internal/subagent/manager_test.go`
- **Severity**: **Low**
- **Category**: Assertion Quality
- **Description**: Tests cover basic CRUD (List, RunningCount, Complete, Spawn) but miss:
  - No test for `Cancel()` on a running agent
  - No test for `CancelAll()` (which is called on interrupt)
  - No test for concurrent `Complete()` calls
  - `Complete_NotFound` only checks "should not panic" — no assertion on return
- **Suggested improvement**: Add `TestManager_Cancel`, `TestManager_CancelAll`, `TestManager_ConcurrentComplete`.

### LOW-15: Swarm Manager — Missing Assertions on Actual Agent Behavior
- **File**: `internal/swarm/manager_test.go:TestManager_ShutdownTeammate`
- **Severity**: **Low**
- **Category**: Assertion Quality
- **Description**: After shutdown, the test only checks that status is `ShuttingDown`. It doesn't verify that the idle loop actually stopped, that the agent was cancelled, or that resources were cleaned up.
- **Suggested improvement**: Verify the teammate goroutine exits by waiting on a done channel.

---

## 4. Mock & Stub Quality

### MEDIUM-16: Agent `mockProvider` — CountTokens Always Succeeds
- **File**: `internal/agent/agent_test.go:287-289`
- **Severity**: **Medium**
- **Category**: Mock Quality
- **Description**: `mockProvider.CountTokens()` always returns `m.tokenCount, nil` with no way to simulate an error. In production, `CountTokens` can fail (e.g., provider doesn't support it). The `blockingSummaryProvider` and `delayedSummaryProvider` both hardcode `errors.New("not implemented")` for `CountTokens`, which means token-aware context manager logic is never tested with successful token counting through these providers.
- **Suggested improvement**: Add an `err` field to `mockProvider` for `CountTokens`.

### MEDIUM-17: Swarm `mockAgent` — No Error Simulation
- **File**: `internal/swarm/manager_test.go:14-39`
- **Severity**: **Medium**
- **Category**: Mock Quality
- **Description**: `mockAgent.RunStream()` always returns `nil` error. There's no way to test what happens when an agent fails mid-task. The `countingAgent` in `idle_runner_test.go` has the same issue.
- **Suggested improvement**: Add an `err` field or callback for error injection.

### LOW-18: Permission Policy Mocks — Minimal Interface Coverage
- **File**: `internal/agent/agent_coverage_test.go:920-941`
- **Severity**: **Low**
- **Category**: Mock Quality
- **Description**: `askAlwaysPolicy` and `denyAlwaysPolicy` implement the `PermissionPolicy` interface with stubs that return `false` for `IsDangerous`, `true` for `AllowedPath`, and no-op for `SetOverride`. These are acceptable for basic tests but don't exercise the actual `ConfigPolicy` implementation's path checking or dangerous command detection.
- **Suggested improvement**: For key tests (approval flow), use `NewConfigPolicyWithMode` with specific tool rules instead of stubs.

---

## 5. Test Organization

### MEDIUM-19: TUI Test Files — Largest Files Are Very Large
- **Files**: `internal/tui/layout_test.go` (3037 LOC, 154 test functions), `internal/tui/tui_test.go` (3222 LOC, 117 test functions)
- **Severity**: **Medium**
- **Category**: Test Organization
- **Description**: These two files together contain 271 test functions and 6259 LOC. They mix unrelated test categories: keyboard handling, render verification, model state transitions, input processing, and view rendering. Finding a specific test is difficult.
- **Suggested improvement**: Split by concern:
  - `keyboard_test.go` — key binding scenarios
  - `view_render_test.go` — render output verification
  - `model_state_test.go` — model state transitions
  - `input_submit_test.go` — input handling and submission

### LOW-20: Duplicated Test Helper `newTestModel()`
- **Files**: `internal/tui/layout_test.go:46`, `internal/tui/auto_run_integration_test.go:225`, `internal/tui/im_approval_test.go:86`
- **Severity**: **Low**
- **Category**: Test Organization
- **Description**: Three different `newTestModel*` constructor variants exist in different test files. Each has slightly different initialization. This makes it hard to maintain a consistent test model state.
- **Suggested improvement**: Extract to a shared `test_helpers_test.go` file with configurable options.

### LOW-21: Build Tags Consistently Applied
- **Severity**: **Low** (Positive Finding)
- **Category**: Test Organization
- **Description**: Integration tests are properly tagged:
  - `//go:build integration_local` — PTY tests, swarm e2e, A2A local tests
  - `//go:build integration` — A2A mesh tests, provider integration
  - CI runs `go test -tags=!integration ./...` which excludes both

  This is well-organized and consistent.

---

## Summary Statistics

| Category | High | Medium | Low | Total |
|----------|------|--------|-----|-------|
| Coverage Gaps | 3 | 3 | 1 | 7 |
| Test Isolation | 2 | 2 | 0 | 4 |
| Assertion Quality | 0 | 2 | 2 | 4 |
| Mock Quality | 0 | 2 | 1 | 3 |
| Test Organization | 0 | 1 | 2 | 3 |
| **Total** | **5** | **10** | **6** | **21** |

## Priority Actions (Top 5)

1. **[HIGH-8/9]** Replace `time.Sleep` with deterministic synchronization in swarm and TUI tests (357 instances total) — biggest flaky test risk
2. **[HIGH-1]** Add daemon package tests (0% coverage on 1014 LOC of critical user-facing code)
3. **[HIGH-2]** Add agent tool execution error path tests (Execute failure, timeout, invalid result)
4. **[HIGH-3]** Add non-streaming `Run()` agent test
5. **[MEDIUM-16/17]** Enhance mock providers with error injection capabilities

## Positive Findings

- **Agent test suite** is thorough (2646 LOC across 2 files, 219 assertions) with excellent coverage of compaction, autopilot, interrupt, and cancellation paths
- **Permission tests** have good table-driven coverage of mode transitions, dangerous command detection, and sandbox enforcement
- **A2A tests** (3177 LOC) are comprehensive with multi-auth, JWT, mTLS, and mesh e2e coverage
- **Build tags** are consistently applied for integration tests
- **Swarm idle runner** has good test coverage of task board polling, assignment filtering, and lifecycle events
- **IM tests** are extensive (4000+ LOC across 30+ test files) covering multiple adapter types
