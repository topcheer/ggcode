# Adaptive Sampling Control

## Overview

Adaptive sampling dynamically adjusts the LLM's temperature parameter on a per-turn basis, matching the sampling behavior to the current task phase. This is complementary to adaptive effort (which controls reasoning depth) — sampling controls output diversity.

## Motivation

All major coding agents (Claude Code, Cursor, Aider, Codex CLI) use a fixed temperature throughout an entire session. However, the optimal temperature varies significantly by task phase:

- **Exploration/planning**: Slightly higher temperature (0.3-0.5) encourages diverse hypotheses about the codebase.
- **Code editing**: Low temperature (0.0-0.2) for deterministic, precise edits that match existing patterns.
- **Error recovery**: Very low temperature (0.0) to avoid compounding errors with creative but wrong guesses.
- **Creative writing**: Moderate temperature (0.4-0.6) for natural language fluency in commit messages and docs.

## Design

### Unified Task-Phase Monitor

Adaptive sampling and adaptive effort share **one** sliding window over the tool-result stream: the unified task-phase monitor (`internal/agent/task_phase.go`, `taskPhaseWindow`). Both adapters read the same window and the same canonical `phaseSignals` summary, so their phase views cannot diverge (previously each adapter kept a private copy of the window, and the sampling copy lacked the error-filtering fixes #1436-A/#1836 — innocuous read-only failures like a grep miss could force max-determinism temperature).

The canonical signal filter: only errors from source-mutating tools count as recovery signals, and routine param-format retries (e.g. `old_text` not found) are excluded. Read-only failures are ignored by both adapters by construction.

### Phase Detection

The sampling controller classifies the current task phase from the shared monitor's signals:

| Phase | Temperature | Trigger Condition (from shared `phaseSignals`) |
|-------|------------|----------------|
| `phaseExploration` | 0.4 | >50% read-only tools in window |
| `phaseCodeEdit` | 0.1 | File edits comprising >=33% of window |
| `phaseErrorRecovery` | 0.0 | 2+ filtered source-mutating errors in window |
| `phaseCreative` | 0.5 | Creative tools (git_commit) >=50% of window |
| `phaseNone` | — (no change) | Insufficient data |

Priority order: error recovery > code editing > creative > exploration.

### Lifecycle

1. **Record**: After each tool execution, the tool name, error status, and error-text prefix are recorded once on the unified task-phase monitor.
2. **Classify**: Before each LLM call, both adapters compute their recommendation from the shared monitor's signals.
3. **Apply**: If the recommended temperature differs from the current setting by >=0.05, it is applied via `SamplingConfigProvider.SetTemperature()`.
4. **Restore**: After the LLM call completes, the previous temperature is restored.

### User Override

When the user explicitly sets temperature via config (`temperature` field) or slash command, the controller stays dormant. This matches the adaptive effort pattern.

### Provider Support

The `SamplingConfigProvider` interface is implemented by:

- **AnthropicProvider** — sends `temperature` and `top_p` in the MessageNew API request
- **OpenAIProvider** — sends `temperature` and `top_p` in the Chat Completion API request  
- **GeminiProvider** — sends `temperature` and `top_p` in the GenerateContent config

When a provider does not implement this interface (e.g., Copilot), the controller is a no-op.

## Files

| File | Purpose |
|------|---------|
| `internal/agent/adaptive_sampling.go` | Sampling policy: phase detection, temperature recommendation, apply/restore |
| `internal/agent/task_phase.go` | Unified task-phase monitor: shared sliding window + canonical signal filter |
| `internal/agent/adaptive_sampling_test.go` | Unit tests for phase classification, override, sliding window |
| `internal/provider/provider.go` | `SamplingConfigProvider` interface definition |
| `internal/provider/anthropic.go` | Anthropic temperature/topP in API request |
| `internal/provider/openai.go` | OpenAI temperature/topP in API request |
| `internal/provider/gemini.go` | Gemini temperature/topP in API request |

## Relationship to Adaptive Effort

| Aspect | Adaptive Effort | Adaptive Sampling |
|--------|----------------|-------------------|
| Controls | Reasoning depth (thinking budget) | Output diversity (temperature) |
| Interface | `ReasoningEffortProvider` | `SamplingConfigProvider` |
| Granularity | low/medium/high | 0.0-0.5 continuous |
| Input | Shared `taskPhaseWindow` signals | Shared `taskPhaseWindow` signals |
| Composable | Yes — both can be active simultaneously | Yes |

Both controllers run before each `streamChatResponse` call and restore their respective parameters afterward. The unified monitor follows the metacognitive "one meta-level monitor, many object-level controllers" pattern (Nelson & Narens 1990; arXiv:2601.01743 §unified control layer), so all compute-allocation policies allocate from the same ground truth.
