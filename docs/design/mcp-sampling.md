# MCP Sampling (Server-to-Client LLM Requests)

## Background

The MCP (Model Context Protocol) specification defines a `sampling/createMessage` method that allows servers to request LLM completions from the client. This is a bidirectional capability: the client calls server tools, and the server can ask the client to generate text using the user's configured LLM.

Common use cases:
- Servers that need to summarize, classify, or transform data using LLM reasoning
- Servers that generate commit messages, PR descriptions, or code summaries
- Servers that perform multi-step reasoning over their own data

## Prior State

Before this implementation, ggcode's MCP client did not:
1. Advertise `sampling` capability during the initialize handshake
2. Handle `sampling/createMessage` requests from servers
3. Have any mechanism to route server-initiated requests to the LLM provider

This meant any MCP server that called `sampling/createMessage` would receive a `-32601 method not found` error, silently breaking server features that depend on sampling.

## Implementation

### Architecture

```
MCP Server → sampling/createMessage → MCP Client → SamplingHandler → Provider.Chat() → Response
```

The flow:
1. MCP server sends `sampling/createMessage` JSON-RPC request
2. `Client.readResponse()` detects a `*Request` message, dispatches to `handleServerRequest()`
3. `handleServerRequest()` routes `sampling/createMessage` to `handleSampling()`
4. `handleSampling()` calls the registered `SamplingHandler` callback
5. The handler converts MCP messages to provider messages and calls `Provider.Chat()`
6. The result is formatted as a `SamplingResult` and written back as a JSON-RPC response

### Key Components

**`internal/mcp/sampling.go`** — Types and interfaces:
- `SamplingParams` / `SamplingResult` — MCP spec-compatible request/response types
- `SamplingHandler` — `func(ctx, params) (*result, error)` interface
- `EffectiveMaxTokens()` — clamps requested tokens to 4096 max
- `ParseSamplingParams()` — deserializes from JSON-RPC params

**`internal/mcp/client.go`** — Client integration:
- `ClientCaps.Sampling` field — advertised during initialize when handler is set
- `Client.SetSamplingHandler()` — registers the handler
- `Client.handleSampling()` — dispatches requests to the handler with 60s timeout

**`internal/agentruntime/mcp_sampling.go`** — Provider-backed handler:
- `SetSamplingProvider()` — lazily sets the LLM provider (called from `SetConfigAgent`)
- `mcpSamplingHandler` — converts MCP messages → provider messages, calls `Chat()`, returns result

**`internal/plugin/mcp_loader.go`** — Plugin wiring:
- `MCPPlugin.SetSamplingHandler()` — stores handler, propagates to each new client
- `MCPManager.SetSamplingHandler()` — propagates to all plugins

### Design Decisions

1. **Lazy provider wiring**: The LLM provider isn't available at `BuildInteractiveRuntimeCore()` time (MCP connects before the agent is created). Uses a package-level variable set via `SetSamplingProvider()` when `SetConfigAgent()` is called.

2. **Token cap**: Max tokens capped at 4096 (`MaxSamplingTokens`) to prevent servers from consuming excessive token budget. Servers that request more get silently clamped.

3. **Timeout**: 60-second context timeout on sampling to prevent blocking the MCP read loop indefinitely.

4. **No tools in sampling**: Sampling calls `Provider.Chat()` with `nil` tools — the server gets a pure text completion, not tool-calling capability.

5. **Automatic opt-in**: Sampling is advertised automatically when a handler is registered. No user configuration needed.
