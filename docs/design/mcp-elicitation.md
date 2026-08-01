# MCP Elicitation (Server-to-User Input)

## Overview

Elicitation is an MCP protocol feature (introduced in 2025-06-18) that allows servers to request structured information directly from the user. Unlike sampling (which asks the LLM to generate text), elicitation asks the human — making it essential for:

- **Credentials**: API keys, tokens, passwords that servers can't hardcode
- **Configuration choices**: Deploy target, environment selection, feature flags
- **Confirmations**: Approval prompts for sensitive server-side operations
- **Free-form input**: Bug descriptions, project names, custom parameters

## Protocol Flow

```
MCP Server                    ggcode Client                    User
    |                              |                              |
    |--- elicitation/create ------>|                              |
    |   {message, requestedSchema} |                              |
    |                              |--- validate schema           |
    |                              |--- convert to AskUserRequest |
    |                              |--- route via broker -------->|
    |                              |                              |
    |                              |<-- user response ------------|
    |                              |--- map to ElicitationResult  |
    |<-- {action, content} --------|                              |
```

### Request

```json
{
  "method": "elicitation/create",
  "params": {
    "message": "Please provide your deployment configuration",
    "requestedSchema": {
      "type": "object",
      "properties": {
        "region": {
          "type": "string",
          "enum": ["us-east", "us-west", "eu", "asia"],
          "description": "Deployment region"
        },
        "confirm": {
          "type": "boolean",
          "description": "Confirm production deployment"
        }
      },
      "required": ["region"]
    }
  }
}
```

### Response (Accept)

```json
{
  "action": "accept",
  "content": {
    "region": "eu",
    "confirm": true
  }
}
```

### Response (Decline)

```json
{
  "action": "decline"
}
```

## Implementation

### Layer Architecture

```
 elicitation.go (mcp package)
   - Types: ElicitationParams, ElicitationResult, ElicitationHandler
   - Schema validation: ValidateElicitationSchema
   - Security: MaxElicitationFields=20, primitive types only
       │
       ▼
 client.go (mcp package)
   - handleElicitation: dispatches elicitation/create requests
   - SetElicitationHandler: registers handler
   - Capability advertisement: elicits in ClientCaps during initialize
   - 5-minute bounded timeout
       │
       ▼
 mcp_loader.go (plugin package)
   - MCPPlugin.SetElicitationHandler: propagates to client
   - MCPManager.SetElicitationHandler: propagates to all plugins
       │
       ▼
 mcp_elicitation.go (agentruntime package)
   - mcpElicitationHandler: converts to AskUserRequest
   - Routes through InteractionBroker (same path as ask_user tool)
   - Maps AskUserResponse back to ElicitationResult
       │
       ▼
 interactive_core.go (agentruntime package)
   - Wires handler into MCPManager at startup
```

### Schema Mapping

MCP elicitation schemas are a subset of JSON Schema. ggcode maps them to the existing `AskUserQuestion` system:

| MCP Schema Type | AskUser Kind | Notes |
|-----------------|-------------|-------|
| `string` (no enum) | `text` | Free-form text input |
| `string` (with enum) | `single` | Choice selection from enum values |
| `boolean` | `single` | Yes/No choice |
| `number`, `integer` | `text` | Free-form text (parsed to number) |

### Security Considerations

1. **Schema validation**: Server-provided schemas are validated before any user interaction. Only primitive types (string, number, integer, boolean) are accepted. Complex types (arrays, nested objects) are rejected.

2. **Field count limit**: Maximum 20 properties per schema to prevent UI overwhelm.

3. **Untrusted content**: The server-provided `message` and field descriptions are treated as untrusted content — they are displayed to the user but never executed or interpreted.

4. **Non-interactive fallback**: When no InteractionBroker is available (e.g., headless mode, cron runner), elicitation requests are rejected with an error, preventing silent failures.

5. **Bounded timeout**: The handler has a 5-minute timeout to prevent blocking the MCP read loop indefinitely.

## Competitor Analysis

| Feature | ggcode | Claude Desktop | Cursor | Cline |
|---------|--------|---------------|--------|-------|
| Elicitation support | Yes | Yes | Yes | Yes |
| Schema validation | Yes (capped at 20 fields) | Yes | Limited | Limited |
| Non-interactive fallback | Rejects gracefully | N/A | N/A | N/A |
| UI routing | Multi-surface (TUI, IM, desktop, mobile) | Desktop only | Desktop only | Desktop only |

## Files Changed

- `internal/mcp/elicitation.go` — Types, handler interface, schema validation
- `internal/mcp/client.go` — Client handler, capability advertisement, request dispatch
- `internal/mcp/elicitation_test.go` — Unit tests (10 test cases)
- `internal/plugin/mcp_loader.go` — Plugin and manager wiring
- `internal/agentruntime/mcp_elicitation.go` — Handler implementation via InteractionBroker
- `internal/agentruntime/interactive_core.go` — Startup wiring
- `docs/guide/mcp.md` — User guide section
