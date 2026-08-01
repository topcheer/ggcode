# MCP Dynamic Notifications & Capability Tracking

## Context

The MCP (Model Context Protocol) specification defines several server-to-client notifications and capabilities that enable dynamic, reactive MCP server behavior. Prior to this change, ggcode's MCP client silently dropped all server-initiated notifications and only tracked the `tools` capability. This meant:

- Dynamic servers that add/remove tools at runtime were not reflected until restart
- Server logging (`notifications/message`) and progress updates were invisible
- Resource list changes were ignored
- The client couldn't gate feature requests on actual server capabilities (e.g., calling `logging/setLevel` on a server that doesn't support logging)

## Design

### Notification Processing

Added a `notificationHandler` callback to `mcp.Client`:

```go
func (c *Client) SetNotificationHandler(h func(method string, params json.RawMessage))
```

The `readResponse` loop now calls `processNotification()` for each `*Notification` message instead of silently continuing. When no handler is registered, the behavior is unchanged (notifications are skipped).

### Capability Tracking

Expanded `ServerCaps` to capture all MCP capability objects:

```go
type ServerCaps struct {
    Tools      *ToolsCapability
    Resources  *ResourcesCapability
    Prompts    *PromptsCapability
    Logging    *struct{}
    Completion *struct{}
}
```

Helper methods gate feature requests:
- `HasToolsListChanged()` — checks if dynamic tool refresh is supported
- `HasLogging()` — gates `logging/setLevel` calls
- `HasResourceSubscribe()` — gates resource subscription requests

### Plugin-Level Integration

`MCPPlugin.setupNotificationHandler()` registers a handler that:
1. **`notifications/tools/list_changed`** — triggers `refreshTools()`, which re-fetches the tool list, unregisters old tools, creates a new adapter, and re-registers updated tools
2. **`notifications/resources/list_changed`** — triggers `refreshResources()`
3. **`notifications/message`** — forwards server logs to ggcode's debug log
4. **`notifications/progress`** — logged for visibility

### Initialize Handshake

Updated to declare `roots.listChanged: true` in client capabilities, signaling to servers that ggcode can handle root changes. Server capabilities are cached after initialize for feature gating.

## Files Changed

- `internal/mcp/client.go` — notification handler, SetLevel, expanded ServerCaps, capability helpers
- `internal/mcp/client_test.go` — 6 new tests
- `internal/plugin/mcp_loader.go` — notification handler wiring, refreshTools, refreshResources
- `docs/guide/mcp.md` — user-facing documentation

## Future Extensions

- **MCP Sampling**: `sampling/createMessage` lets MCP servers request LLM completions from the client. Requires routing through the agent's provider.
- **MCP Elicitation**: Server-initiated user input requests. Requires TUI integration.
- **Resource Subscriptions**: `resources/subscribe` + `notifications/resources/updated` for live resource tracking.
