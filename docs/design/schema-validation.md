# Schema-Constrained Tool Argument Validation

## Problem

Weak models (open-weight models via goolm, third-party endpoints, smaller models) frequently
produce tool arguments that violate the declared JSON Schema:

1. **Invalid enum values** — e.g. `{"action": "xyz"}` when the schema declares `"enum": ["read", "write", "delete"]`
2. **Out-of-range numbers** — e.g. `{"offset": -5}` when the schema declares `"minimum": 0`
3. **String length violations** — e.g. `{"path": "ab"}` when the schema declares `"minLength": 3`
4. **Hallucinated parameters** — e.g. `{"path": "/tmp", "recursive": true}` when the tool has no `"recursive"` parameter

Each of these wastes at least one full agent loop iteration: the tool either fails with a
confusing error, silently misbehaves, or triggers a Go json.Unmarshal strict-mode failure.

## Existing Layers (before this work)

| Layer | File | What it catches |
|-------|------|-----------------|
| Type coercion | `tool/arg_coercion.go` `CoerceArguments` | String→int/number/bool type mismatches |
| Required param validation | `tool/arg_coercion.go` `ValidateRequiredParams` | Missing required fields |

Neither layer validates enum values, numeric/string constraints, or unknown parameters.

## Solution

Three new functions in `internal/tool/schema_validation.go`, integrated into the
`executeTool` pipeline in `internal/agent/agent_tool.go` after coercion and required-param
validation:

### 1. `ValidateSchemaConstraints(schema, args) string`

Validates:
- **enum**: value must match one of the allowed options (supports string and numeric enums)
- **minimum / maximum**: numeric bounds (inclusive)
- **exclusiveMinimum / exclusiveMaximum**: numeric bounds (exclusive)
- **minLength / maxLength**: string length bounds

Returns a human-readable error string for the first violation, or `""` if all constraints pass.
The error message is passed directly to the model as a tool error, enabling immediate correction.

### 2. `StripUnknownParams(schema, args) json.RawMessage`

Removes arguments that are not declared in the tool's `properties` schema. Respects
`"additionalProperties": true` (leaves extras alone when explicitly allowed).

This is a defensive measure — most Go tools silently ignore unknown JSON keys, but some
use strict deserialization or map-based processing where unexpected keys cause issues.

### 3. Integration in `executeTool`

The validation pipeline in `agent_tool.go` now runs in this order:

```
1. CoerceArguments     — fix type mismatches (string→int, etc.)
2. ValidateRequiredParams — check for missing required fields
3. ValidateSchemaConstraints — check enum, min/max, minLength/maxLength (NEW)
4. StripUnknownParams   — remove hallucinated parameters (NEW)
5. Pre-tool hooks      — user-defined pre-execution hooks
6. Tool.Execute         — actual execution
7. Post-tool hooks     — user-defined post-execution hooks
```

## Competitive Analysis

| Feature | Claude Code | Cursor | Aider | ggcode |
|---------|-------------|--------|-------|--------|
| Type coercion | No | No | No | Yes |
| Required param check | Partial | No | No | Yes |
| Enum validation | No | No | No | **Yes (new)** |
| Numeric bounds check | No | No | No | **Yes (new)** |
| String length check | No | No | No | **Yes (new)** |
| Unknown param strip | No | No | No | **Yes (new)** |

Most AI coding assistants rely entirely on the LLM's own tool-use fidelity. ggcode's
multi-layer pre-execution validation pipeline is a differentiated advantage when working
with weaker models, especially open-weight models served via goolm.

## Testing

`internal/tool/schema_validation_test.go` — 17 test cases covering:
- Valid/invalid enum values (string and numeric)
- Minimum/maximum/exclusive bounds
- MinLength/maxLength constraints
- Unknown param stripping with and without `additionalProperties`
- Edge cases: nil schema, empty args, unparseable JSON

## Research Context

This implements the "Tool Call Structural Validation" pattern described in:
- "ToolACE: Winning the Principles of Agent Tool Use" (2025) — emphasizes schema-conformant
  tool call generation and validation
- "API-Bank: A Comprehensive Benchmark for Tool-Augmented LLMs" (2026) — shows that
  parameter-level accuracy is a major differentiator between strong and weak models
