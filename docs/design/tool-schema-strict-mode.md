# Tool Schema Strict Mode Analysis

> Status: **Deferred** — documented for future implementation.

## Background

We investigated adding `strict: true` to tool definitions to guarantee LLMs always fill required fields (especially the `description` parameter used for TUI activity labels).

### Problem Statement

Without `strict: true`, LLMs may omit `required` fields in tool calls. Session data confirmed that `read_file`/`edit_file`/`write_file` never included the `description` parameter despite it being marked `required` in the JSON Schema, while `run_command` consistently included it.

Root cause: `required` in JSON Schema is a **semantic hint**, not a runtime enforcement. LLMs can freely ignore it.

## What `strict: true` Does

Both OpenAI and Anthropic support `strict: true` at the **tool definition level** (not per-field). It uses **grammar-constrained sampling (CFG)** at inference time — compiling the JSON Schema into a context-free grammar that constrains token probabilities at each step, guaranteeing the generated JSON strictly matches the schema.

### Guarantees

- All `required` fields are always present
- Types match exactly (no `"2"` instead of `2`)
- No extra properties beyond the schema
- Tool name is always valid

## SDK Support

| Provider | SDK | Strict Field | Notes |
|---|---|---|---|
| OpenAI | `sashabaranov/go-openai` v1.41.2 | `FunctionDefinition.Strict bool` (`json:"strict,omitempty"`) | Works for GPT-4o+ |
| Anthropic | `anthropics/anthropic-sdk-go` v1.29.0 | `ToolParam.Strict param.Opt[bool]` (`json:"strict,omitzero"`) | Needs beta header `anthropic-beta: structured-outputs-2025-11-13` |
| Gemini | `google.golang.org/genai` v1.52.1 | No strict concept | N/A |
| OpenAI-compatible (DeepSeek, ZAI, etc.) | Same as OpenAI | Field exists in SDK | **Unknown behavior** — may ignore or return 400 |

### Additional Requirements

When `strict: true` is set, the JSON Schema **must** include `"additionalProperties": false` at the top level (and on all nested objects). This is enforced by both OpenAI and Anthropic APIs.

Current tool schemas do **not** include `"additionalProperties": false`.

## Implementation Plan (When Ready)

### Files to Modify

1. **`internal/provider/provider.go`** — Add `Strict bool` to `ToolDefinition`
2. **`internal/provider/openai.go`** — Set `Strict: t.Strict` in `convertTools()`
3. **`internal/provider/anthropic.go`** — Set `Strict` + inject `additionalProperties: false` into schema
4. **`internal/provider/gemini.go`** — No change (Gemini doesn't support strict)
5. **All tool `Parameters()` JSON schemas** — Add `"additionalProperties": false`

### Safety Analysis

- **OpenAI SDK**: `Strict bool` with `json:"strict,omitempty"` — `false` (zero value) is not serialized. Safe by default.
- **Anthropic SDK**: `Strict param.Opt[bool]` with `json:"strict,omitzero"` — zero value not serialized. Safe by default.
- **Gemini**: No strict field in SDK or API. Completely unaffected.
- **Third-party OpenAI-compatible APIs**: **RISK** — some may return HTTP 400 when receiving `strict: true`. Needs per-provider testing.

### Recommended Approach

1. Add a `StrictMode` config option (per-vendor or global) so it can be toggled per-endpoint
2. Default to `false` — only enable when the endpoint is known to support it
3. For Anthropic, also need to set the beta header on the request
4. For third-party OpenAI-compatible endpoints, add a `supports_strict` vendor flag to avoid 400 errors

## Current Soft Enforcement Strategy

Before implementing strict mode, we applied these soft enforcement measures:

1. **Schema fix**: All 21 tools with `description` parameter now have it correctly inside `properties` (was broken — either invalid JSON or at root level)
2. **Required enforcement**: `description` is in every tool's `required` array
3. **Description text emphasis**: Field description starts with "REQUIRED." and ends with "You MUST always provide this field."
4. **TUI rendering fix**: `tool_labels.go` now checks `description` for 15 tools that previously ignored it

This soft approach may be sufficient — `run_command` proved LLMs will comply with `required` + strong descriptions. The schema was previously broken for file tools, so historical non-compliance doesn't predict future behavior with fixed schemas.

## References

- [OpenAI Structured Outputs](https://platform.openai.com/docs/guides/structured-outputs)
- [Anthropic Strict Tool Use](https://docs.anthropic.com/en/docs/agents-and-tools/tool-use/strict-tool-use)
- [Anthropic Tool Definition Best Practices](https://docs.anthropic.com/en/docs/agents-and-tools/tool-use/define-tools)
