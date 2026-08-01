# Gemini Reasoning Effort & Tool Choice Support

## Status
Implemented

## Context

The `gemini` provider (`internal/provider/gemini.go`) previously lacked support
for two optional provider capabilities that `openai` and `anthropic` already
implemented:

1. **Reasoning Effort** (`ReasoningEffortProvider`) — controlling how many
   thinking tokens the model allocates before responding.
2. **Tool Choice** (`ToolChoiceProvider`) — controlling whether the model
   is allowed/required/disallowed from calling tools.

When a user configured `reasoning_effort: high` or `tool_choice: required`
on a Gemini endpoint, the values were silently ignored — the Gemini provider
didn't implement either interface, so the registry never attempted to set them.

## Design

### Reasoning Effort → ThinkingConfig

Gemini 2.5+ models support a `thinkingConfig.thinkingBudget` parameter in the
`GenerateContentConfig`. The effort levels map to token budgets as fractions
of `maxTokens`:

| Effort | Budget |
|--------|--------|
| `low` | ~25% of maxTokens (min 512) |
| `medium` | ~50% of maxTokens |
| `high` | ~75% of maxTokens |

The budget is clamped to `[512, maxTokens-1]` to satisfy API constraints.
If `maxTokens` is too small (≤ 512), thinking is disabled entirely with
budget 0.

The adaptive cap value is used instead of `maxTokens` when available, so the
thinking budget scales with the dynamically discovered output limit.

### Tool Choice → FunctionCallingConfig.Mode

| Config Value | Gemini Mode | Behavior |
|-------------|-------------|----------|
| `auto` | `AUTO` | Model decides whether to call a function |
| `required` | `ANY` | Model must call one of the provided functions |
| `none` | `NONE` | Model must not call any function |

Tool choice is only applied when tools are present in the request.

### Implementation

Both `Chat` and `ChatStream` now call two helper methods after building the
base config:

```go
p.applyReasoningEffort(config)
p.applyToolChoice(config, tools)
```

These methods are no-ops when the respective field is empty, preserving
backward compatibility.

## Files Changed

- `internal/provider/gemini.go` — Added `reasoningEffort` and `toolChoice`
  fields, `SetReasoningEffort`/`ReasoningEffort`/`SetToolChoice`/`ToolChoice`
  methods, and `applyReasoningEffort`/`applyToolChoice` helpers. Updated
  `CloneWithModel` to preserve both fields.
- `internal/provider/registry.go` — Pass `resolved.ReasoningEffort` and
  `resolved.ToolChoice` to the Gemini provider on construction.
- `internal/provider/gemini_test.go` — Added unit tests for reasoning effort,
  tool choice, and clone preservation.
- `docs/guide/providers.md` — Added user-facing documentation for both features.
