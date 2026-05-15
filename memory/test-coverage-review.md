# GGCode Test Coverage & Quality Review Report

**Reviewer**: test-reviewer | **Date**: 2025-07-12  
**Scope**: `internal/` (41 packages)

---

## 1. Executive Summary

| Metric | Value |
|--------|-------|
| Total packages | 41 |
| Total source LOC | 142,188 |
| Total test LOC | 108,923 |
| Test-to-source ratio | **0.77** (good) |
| Total test functions | **3,973** |
| Total benchmark functions | **18** |
| Zero-test packages | **4** (daemon, markdown, version, im/stt) |
| Packages with >80% coverage | **15/37** (40%) |
| Packages with <50% coverage | **4/37** (auth 42.5%, acp 45.2%, subagent 46.7%, provider 50.3%) |
| Build failures | **2** (im — libolm C dependency, tui — same root cause) |

**Overall Test Quality Score: B (7.5/10)**

The project has a strong testing culture with 3,973 test functions across 37 tested packages. Test-to-source LOC ratio of 0.77 is commendable for a Go CLI project of this complexity. However, there are notable gaps: `internal/daemon/` has zero tests despite containing critical process management logic, benchmark coverage is thin (only 4 of 41 packages), and some core packages (provider, auth) have sub-50% statement coverage.

---

## 2. Per-Package Coverage Report

### 2.1 Statement Coverage (from `go test -cover`)

| Package | Coverage | Src LOC | Test LOC | Tests | Assessment |
|---------|----------|---------|----------|-------|------------|
| diff | **97.7%** | 183 | 57 | 4 | Excellent |
| cost | **94.6%** | 280 | 226 | 13 | Excellent |
| checkpoint | **83.7%** | 141 | 135 | 7 | Excellent |
| context | **84.0%** | 1,182 | 1,003 | 33 | Excellent |
| commands | **83.9%** | 905 | 555 | 32 | Excellent |
| safego | **82.4%** | 79 | 81 | 6 | Excellent |
| swarm | **83.3%** | 1,120 | 2,772 | 69 | Excellent |
| memory | **80.8%** | 546 | 390 | 12 | Good |
| util | **83.6%** | 310 | 522 | 24 | Good |
| hooks | **81.5%** | 201 | 121 | 7 | Good |
| install | **73.6%** | 619 | 473 | 17 | Good |
| config | **77.3%** | 6,564 | 6,130 | 230 | Good |
| task | **77.8%** | 256 | 604 | 20 | Good |
| agent | **78.4%** | 2,030 | 3,175 | 91 | Good |
| harness | **78.1%** | 7,167 | 10,660 | 311 | Good |
| debug | **78.1%** | 854 | 447 | 14 | Good |
| restart | **76.6%** | 318 | 137 | 7 | Adequate |
| cron | **74.5%** | 439 | 419 | 34 | Adequate |
| knight | **74.6%** | 7,620 | 4,533 | 151 | Adequate |
| permission | **74.4%** | 733 | 410 | 24 | Adequate |
| extract | **74.6%** | 1,217 | 542 | 34 | Adequate |
| lsp | **70.1%** | 2,623 | 2,811 | 94 | Adequate |
| a2a | **67.7%** | 4,136 | 6,478 | 184 | Adequate |
| session | **66.9%** | 895 | 638 | 20 | Adequate |
| update | **64.1%** | 428 | 381 | 20 | Adequate |
| stream | **63.8%** | 2,538 | 1,878 | 75 | Adequate |
| plugin | **63.8%** | 1,216 | 884 | 41 | Adequate |
| chat | **59.5%** | 2,149 | 1,107 | 53 | Moderate |
| webui | **59.5%** | 2,090 | 3,550 | 120 | Moderate |
| tool | **60.0%** | 11,409 | 8,206 | 335 | Moderate |
| image | **53.8%** | 414 | 244 | 15 | Moderate |
| mcp | **51.2%** | 2,664 | 1,718 | 75 | Moderate |
| provider | **50.3%** | 3,664 | 2,273 | 91 | **Low** |
| subagent | **46.7%** | 858 | 455 | 31 | **Low** |
| acp | **45.2%** | 3,123 | 2,771 | 79 | **Low** |
| auth | **42.5%** | 2,003 | 1,334 | 70 | **Low** |
| daemon | **0.0%** | 1,050 | 0 | 0 | **CRITICAL** |
| markdown | **0.0%** | 196 | 0 | 0 | ZERO |
| version | **0.0%** | 21 | 0 | 0 | ZERO (trivial) |
| im/stt | **0.0%** | - | 0 | 0 | ZERO |
| tui | **BUILD FAIL** | 44,141 | 22,280 | 900 | Cannot measure |

### 2.2 File Ratio (Test Files / Source Files)

| Category | Packages | Notes |
|----------|----------|-------|
| **GOOD** (ratio >= 1.0) | 11 | auth, checkpoint, config, context, harness, im, lsp, safego, stream, swarm, task, util, webui |
| **MODERATE** (0.5-0.99) | 26 | Most packages |
| **ZERO** (0 test files) | 4 | daemon, markdown, version, im/stt |

---

## 3. Key Path Coverage Assessment

### 3.1 Agent Core Loop (`internal/agent/`) — 78.4% coverage

**Strengths:**
- 91 test functions across 4 test files (3,175 test LOC)
- Comprehensive coverage of `executeToolWithPermission` — tests deny policy, allow policy, ask-approved, ask-denied, no handler, cancelled context
- Thorough `computeFileChange` testing — edit/write for existing files, new files, invalid JSON, unknown tool
- Good autopilot loop guard testing with Chinese + English text patterns
- Excellent vendor-specific error detection coverage (`isPromptTooLongError` covers OpenAI, Anthropic, Gemini, Groq, Mistral, Together, Moonshot, ZAI, Copilot, Ark, Novita, and future vendors like Bedrock, Ollama, Cohere, etc.)
- Memory target path testing with deduplication, URL exclusion, empty path handling

**Weaknesses:**
- **No direct test for the main `Run()`/`RunStream()` loop** — the core agent loop that orchestrates LLM calls + tool execution is only tested via integration tests (behind `//go:build integration_local`)
- Tool execution hooks (`pre/post hooks`) have minimal coverage — `TestAgent_SetHookConfig` just verifies it doesn't crash
- No test for compaction/summarization flow (auto-compact logic)
- `agent_autopilot.go` continuation logic is only indirectly tested

### 3.2 Provider Adapters (`internal/provider/`) — 50.3% coverage

**Strengths:**
- 91 test functions covering OpenAI message conversion, Anthropic streaming, Gemini formatting
- Good table-driven tests for message conversion edge cases
- Adaptive capacity probing tested with timeout scenarios
- Context inference tested for max_tokens/temperature detection

**Weaknesses:**
- **No unit test for actual HTTP request construction** — tests focus on message conversion but not the HTTP layer
- Streaming response parsing has limited coverage
- Retry logic is not directly tested
- Only `openai_test.go` has tests; Anthropic/Gemini/Copilot adapters rely on integration tests
- `provider.go` registry logic untested

### 3.3 Tool Execution (`internal/tool/`) — 60.0% coverage

**Strengths:**
- 335 test functions — largest test suite after tui
- Excellent `command_gate_test.go` with table-driven tests and benchmark
- `labels_test.go` thoroughly tests tool label resolution
- Good coverage of `run_command` with mock process execution

**Weaknesses:**
- Many tools (file ops, search, git) are integration-tested via PTY harnesses
- `builtin.go` tool registration not directly tested
- MCP tool adaptation layer has sparse coverage

### 3.4 Harness Engine (`internal/harness/`) — 78.1% coverage

**Strengths:**
- **311 test functions** — most thoroughly tested subsystem
- 10,660 test LOC vs 7,167 source LOC (ratio 1.49)
- Comprehensive mock runners: `fakeRunner`, `streamingRunner`, `sequenceRunner`, `blockingRunner`, `writingRunner`
- Task queue, dependency tracking, worktree management, promotion, review all covered
- Excellent table-driven tests in `router_test.go` (10 test tables)
- E2E tests behind proper build tags

**Weaknesses:**
- LLM classifier uses mock provider; real LLM classification tested only in integration
- Some monitor/drift detection paths untested

### 3.5 IM Adapters (`internal/im/`) — BUILD FAIL (libolm C dep)

**Strengths:**
- **630 test functions** — largest test suite
- 18,523 test LOC covering QQ, Telegram, Discord, Slack, DingTalk, Feishu, WeChat, Nostr, Matrix, IRC, Signal, WhatsApp, Mattermost, Twitch adapters
- Good unit test coverage for adapter message formatting
- Runtime mute/unmute logic thoroughly tested
- Echo suppression tested in `runtime_mute_test.go`
- E2E tests properly tagged with `//go:build integration_service`

**Weaknesses:**
- Build dependency on `libolm` (C library for Matrix encryption) prevents running tests without system deps
- Many adapter tests are mock-heavy but don't test actual network I/O error handling
- Message splitting has benchmarks but other performance-critical paths don't

### 3.6 TUI (`internal/tui/`) — BUILD FAIL (same libolm dep)

**Strengths:**
- **900 test functions**, 22,280 test LOC
- Comprehensive keyboard interaction tests (1,489 LOC)
- Layout tests (3,037 LOC) — model picker, provider picker, panel rendering
- PTY-based integration tests for subcommands, panels, QR overlays
- IM panel, stream panel, model coverage all tested

**Weaknesses:**
- Cannot measure coverage due to build failure
- PTY tests are behind `integration_local` build tag
- Many tests are PTY-based (fragile, slow) rather than pure unit tests
- Mouse event handling tests use `t.Skip("file browser not available")` — conditional

---

## 4. Test Quality Assessment

### 4.1 Test Naming — **Good (8/10)**

Tests follow Go conventions:
- `TestOpenAIConvertMessages_SystemText` — clear function + scenario
- `TestAgent_ApprovalHandler_DenyWithoutHandler` — component + behavior
- `TestShouldAutopilotKeepGoing` — function name = test description
- `TestComputeFileChange_EditFile_Nonexistent` — function + edge case

No generic names like `TestFoo` without context.

### 4.2 Table-Driven Tests — **Good (7/10)**

226 instances of `tests := []struct` across 98 files. Particularly well-used in:
- `agent_coverage_test.go` (10 tables)
- `permission/mode_test.go` (2 tables)
- `provider/openai_test.go` (2 tables)
- `harness/router_test.go` (10 tables)
- `im/qq_adapter_unit_test.go` (13 tables)

**Improvement opportunity:** Some packages (tui, webui) use individual test functions where table-driven would be more appropriate.

### 4.3 Mock/Stub Quality — **Good (7.5/10)**

19 mock types found across the codebase:
- `mockProvider` (agent, context, acp) — implements `provider.Provider`
- `mockTool` (agent, plugin) — implements `tool.Tool`
- `mockChatBridge` (webui) — implements `ChatBridge`
- `mockPolicy` (tool) — implements permission policy
- `fakeRunner`, `streamingRunner`, `sequenceRunner`, `blockingRunner`, `writingRunner` (harness) — excellent variety
- `mockInteractiveAdapter` (im) — for testing IM flows

**Notable:** Tests define mocks inline in test files rather than using mockgen or similar frameworks. This is idiomatic Go but means mock implementations are duplicated across packages.

### 4.4 Test Isolation — **Good (8/10)**

- **No `TestMain` functions** — no global state setup/teardown
- Tests use `t.TempDir()` for filesystem operations
- No shared global state between tests
- Integration tests use build tags, not runtime flags (except `testing.Short()` for PTY tests)
- Each test constructs its own agent/provider instance

**Concern:** The `im` package has `runtime_test.go` (1,054 LOC) and `runtime_mute_test.go` (1,131 LOC) which may share runtime state through the test process environment.

### 4.5 Integration Test Marking — **Excellent (9/10)**

Three tiers of integration test tags:
1. `//go:build integration` — CI integration tests (a2a mesh e2e)
2. `//go:build integration_local` — local integration tests requiring config/API keys
3. `//go:build integration_service` — tests requiring live IM services

Runtime skip conditions:
- API key checks (`ZAI_API_KEY`, `GGCODE_ZAI_API_KEY`)
- Config file presence (`~/.ggcode/ggcode.yaml`)
- External tool availability (`gopls`, `npm`, `dotnet`, `clangd`)
- `testing.Short()` for PTY tests
- `GGCODE_E2E` env var for service tests

CI runs: `go test -tags=!integration ./...` — cleanly excludes all integration tests.

### 4.6 Boundary Condition Testing — **Moderate (6/10)**

**Covered:**
- Empty/nil inputs (messages, paths, JSON)
- Cancelled contexts
- Invalid JSON arguments
- Unknown tools
- File not found
- Oversized prompts (multi-vendor error detection)
- Autopilot loop guard thresholds

**Missing:**
- Concurrent agent access (race conditions)
- Token limit boundary cases
- Very large file handling
- Unicode/encoding edge cases in file operations
- Network timeout/retry scenarios in provider
- Malformed LLM streaming responses

### 4.7 Benchmark Coverage — **Weak (3/10)**

Only **18 benchmark functions** across **4 packages** (out of 41):

| Package | Benchmarks | What's Benchmarked |
|---------|-----------|-------------------|
| stream | 8 | Image encoding, rendering, capture |
| knight | 7 | Skill index, conventions, queue, usage, budget, validation |
| im | 2 | Message splitting |
| tool | 1 | Command gate check |

**Missing benchmarks for critical paths:**
- Agent loop iteration
- Provider HTTP call + response parsing
- Context window compaction
- Session JSONL read/write
- Harness task evaluation
- MCP JSON-RPC communication
- TUI rendering performance

---

## 5. Critical Issues

### 5.1 Zero-Test Packages

| Package | LOC | Risk | Priority |
|---------|-----|------|----------|
| **daemon** | 1,050 | **HIGH** — Process forking, PID file management, follow display, background daemon lifecycle. Contains `ForkIntoBackground()` which is security-sensitive (process management). | **P0** |
| markdown | 196 | LOW — Single file, likely simple wrapper | P2 |
| version | 21 | NONE — Build-time ldflags injection, trivial package | P3 |
| im/stt | - | LOW — Speech-to-text, likely small | P2 |

### 5.2 Low-Coverage Critical Packages

| Package | Coverage | Risk | Priority |
|---------|----------|------|----------|
| **provider** | 50.3% | **HIGH** — Core LLM communication layer. Retry logic, streaming, error handling untested. | **P1** |
| **auth** | 42.5% | **MEDIUM** — OAuth2 flows, JWT validation, token caching. Complex security-sensitive code. | **P1** |
| **subagent** | 46.7% | **MEDIUM** — Sub-agent spawning, cancellation, timeout. Concurrency-sensitive. | **P1** |
| **acp** | 45.2% | **MEDIUM** — Agent Communication Protocol. | **P2** |

### 5.3 Build Failures

`internal/im` and `internal/tui` fail to build due to `maunium.net/go/mautrix` C dependency (`olm/olm.h` not found). This means:
- 630 im tests + 900 tui tests = **1,530 tests** cannot run without `libolm` installed
- CI likely handles this via separate build tags or pre-installed deps
- **Recommendation:** Make Matrix crypto dependency optional behind a build tag

---

## 6. Improvement Priority Recommendations

### P0 — Immediate (Security + Core Reliability)

1. **Add tests for `internal/daemon/`**
   - `background.go`: Test PID file write/read/cleanup, daemon info serialization, `workDirHash` determinism
   - `follow.go`: Test `ResolveLang`, `PlatformDisplayName`, `FormatFollowToolArgs`, markdown rendering edge cases
   - `ForkIntoBackground` is hard to unit test but `CheckExistingDaemon`, `ReadPIDFile`, `WritePIDFile` are trivially testable
   - Estimated: ~50 test functions, ~800 LOC

2. **Fix build failure for im/tui packages**
   - Gate `mautrix/crypto/libolm` behind `//go:build` tag
   - Or add `libolm` to CI environment

### P1 — High Priority (Core Logic Gaps)

3. **Increase provider test coverage to >70%**
   - Add HTTP mock tests using `httptest.Server` for each adapter
   - Test retry logic with simulated failures
   - Test streaming response parsing with mock HTTP responses
   - Test error response handling (rate limits, auth errors, context overflow)
   - Estimated: ~40 test functions

4. **Increase auth test coverage to >65%**
   - Test JWT validation with RS256/ECDSA keys
   - Test token cache isolation (per-clientID)
   - Test OAuth2 PKCE flow end-to-end with mock server
   - Test JWKS key rotation
   - Estimated: ~30 test functions

5. **Add subagent concurrency tests**
   - Test concurrent sub-agent spawning with semaphore
   - Test `CancelAll()` under load
   - Test timeout handling
   - Estimated: ~15 test functions

### P2 — Medium Priority (Quality Improvements)

6. **Add benchmarks for hot paths**
   - Agent loop single iteration
   - Provider response parsing (large JSON)
   - Session JSONL append/write
   - Context window compaction
   - TUI render cycle
   - Estimated: ~20 benchmarks

7. **Improve tui test architecture**
   - Separate pure logic tests from PTY harness tests
   - More unit tests for individual components (panels, key handlers)
   - Reduce reliance on `integration_local` build tag for core logic

8. **Add property-based/fuzz tests**
   - JSON argument parsing in tool execution
   - Message conversion (OpenAI/Anthropic/Gemini adapters)
   - Context window token counting

### P3 — Lower Priority

9. **Standardize mock locations** — Consider a `internal/testutil/mock` package for shared mocks
10. **Add race detection to CI** — `go test -race ./...`
11. **Coverage regression tracking** — Set minimum coverage thresholds per package

---

## 7. Summary Statistics

```
Total Source Files:       ~334 Go files
Total Test Files:         ~250+ test files
Total Source LOC:         142,188
Total Test LOC:           108,923
Test:Source Ratio:        0.77
Total Test Functions:     3,973
Total Benchmarks:         18
Packages Tested:          37/41 (90.2%)
Zero-Test Packages:       4 (daemon, markdown, version, im/stt)
Avg Statement Coverage:   ~68% (excluding zeros)
Median Coverage:          ~73%
```

---

*Report generated by test-reviewer agent for code-review-team.*
