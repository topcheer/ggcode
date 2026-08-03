# Tool Argument JSON Repair in Agent Pipeline

## Problem

Many OpenAI-compatible backends (vLLM, LiteLLM, goolm) and weaker reasoning models
produce tool-call arguments that are *almost* valid JSON but fail strict parsing.
Common malformations include:

- **Stream truncation**: missing closing braces/brackets when the model's output is cut off
- **Trailing commas**: `{"file_path": "/tmp/test.go",}` (invalid JSON, common from weaker models)
- **Markdown code fences**: arguments wrapped in ` ```json ... ``` ` instead of bare JSON
- **Surrounding prose**: `"Here are the args: {...} hope this works"`
- **Smart/curly quotes**: Unicode left/right quotation marks instead of ASCII `"`

When tool arguments fail JSON parsing, every downstream pre-processor in the agent's
`executeTool` pipeline silently bails out:

1. `CoerceArguments` returns the original input unchanged (its `json.Unmarshal` fails)
2. `ValidateRequiredParams` returns "" (its `json.Unmarshal` fails)
3. `ValidateSchemaConstraints` returns "" (its `json.Unmarshal` fails)
4. `StripUnknownParams` returns the original (same reason)
5. The tool itself fails with a confusing "invalid input" error

This wastes a full agent iteration on a problem that could be deterministically fixed.

## Existing Partial Coverage

`provider.RepairJSON()` already existed in `internal/provider/jsonrepair.go` and was
called in the OpenAI streaming path (`openai.go`). However, it was **not** applied for:

- **Gemini provider**: tool arguments arrive through a different code path
- **Anthropic/Claude provider**: same issue, different streaming format
- **goolm (local models)**: the most common source of malformed JSON
- **Inline tool calls**: when a model embeds a tool call in text rather than structured blocks

## Solution

Added `provider.RepairJSON()` as the **first** argument pre-processing step in the
agent's `executeTool` method, before `CoerceArguments` and all other schema-aware
pre-processors. This ensures ALL providers benefit from JSON repair, not just OpenAI.

### Design

- **Zero-cost fast path**: `json.Valid()` check means already-valid JSON (the common case)
  returns immediately with no allocation or transformation.
- **Provider-agnostic**: works regardless of which provider produced the tool call.
- **No false positives**: the repair function only returns `true` when the repaired output
  passes `json.Valid()`. If repair fails, the original input is passed through unchanged,
  preserving the original error behavior.
- **Logging**: successful repairs are logged via `debug.Log("agent", ...)` for observability.

### Repair Steps (in order)

1. Strip markdown code fences (` ```json ... ``` `)
2. Normalize smart/curly quotes to ASCII quotes
3. Extract JSON object (first `{` to last `}`, removing surrounding prose)
4. Remove trailing commas before `}` and `]`
5. Close unclosed delimiters (append missing `}`, `]`, `"`)

Each step re-checks `json.Valid()` and returns early if the input is now valid.

## Testing

Tests in `internal/agent/agent_tool_repair_test.go` cover:

- All 5 malformation types (trailing comma, code fence, smart quotes, truncation, prose)
- Already-valid JSON fast path (no repair needed)
- Empty/nil/whitespace edge cases
- Integration: repair followed by schema-aware coercion

## Impact

This fix prevents wasted agent iterations for all non-OpenAI providers when models
emit slightly malformed JSON. It is especially impactful for goolm (local models)
and third-party OpenAI-compatible endpoints, where JSON malformations are most common.
