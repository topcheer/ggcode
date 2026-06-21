# MCP Servers

MCP (Model Context Protocol) connects external tools and data sources to ggcode, extending what the agent can do.

## What MCP Does

MCP servers act as bridges — they expose tools, resources, and prompts that ggcode can invoke. Examples: web search, browser automation, database queries, GitHub access.

## Install a Server

### Interactive Wizard

Launch the wizard with no arguments:

```bash
ggcode mcp install
```

### stdio Server

Install a stdio-based MCP server:

```bash
ggcode mcp install my-server stdio npx -y @modelcontextprotocol/server-github
```

### HTTP Server

Install an HTTP-based MCP server:

```bash
ggcode mcp install my-http http https://api.example.com/mcp
```

### With Environment Variables

Pass env vars to the server process:

```bash
ggcode mcp install github stdio \
  --env GITHUB_TOKEN=ghp_xxx \
  -- npx -y @modelcontextprotocol/server-github
```

## Manage Servers

List all configured MCP servers:

```bash
ggcode mcp list
```

Remove a server:

```bash
ggcode mcp uninstall my-server
```

## Pre-configured Servers

The default config includes these servers:

| Server | Purpose |
|--------|---------|
| `cloudflare` | Cloudflare API and docs |
| `web-reader` | Fetch and read web pages |
| `playwright` | Browser automation |
| `zai-mcp-server` | Image analysis and OCR |

## Auto-Start

MCP servers start automatically when ggcode launches. If a server fails to start, a warning is shown in the TUI.

## OAuth Support

For MCP servers that require authentication (e.g., remote HTTP servers like Cloudflare MCP), ggcode handles the full OAuth 2.1 flow automatically — dynamic client registration, PKCE, browser-based authorization, and token refresh.

### Token Storage

OAuth tokens are persisted to `~/.ggcode/provider_auth.json` using two types of keys:

| Key format | Scope | Purpose |
|------------|-------|---------|
| `mcp:<serverName>` | Per-server | Server-specific credential. Takes **priority** on load. |
| `mcp-shared:<issuer>\|<resource>` | Shared (canonical) | Same-issuer fallback for servers that haven't been individually authenticated. |

**Load priority**: `mcp:<serverName>` first, then `mcp-shared:<issuer>|<resource>`.

This dual-key design means:

- **First server** from an OAuth provider (e.g., `cf`) authenticates and seeds both keys.
- **Second server** from the same provider (e.g., `cf2`) automatically reuses the canonical key — no separate auth needed.
- **Reset Auth** on `cf2` deletes only `mcp:cf2` and temporarily skips the canonical fallback, forcing a fresh OAuth flow for a **different account** without affecting `cf`.

### Reset Auth (Switch Account)

To re-authenticate a specific MCP server with a different account:

**TUI**: Open the MCP panel (`m` then select MCP, or use the panel switcher), highlight the server, press `a`.

**Desktop**: Go to Settings > MCP Servers, click the key icon next to an HTTP/WS server.

This will:
1. Unregister all existing tools (clears stale metadata like embedded account IDs).
2. Disconnect the current connection.
3. Delete the server-name credential (`mcp:<serverName>`).
4. Temporarily skip the canonical (shared) credential.
5. Reconnect — the missing token triggers a 401, which starts a fresh OAuth flow.
6. The browser opens the provider's authorization page. **Log out of the old account and log into the desired account before authorizing.**
7. The new token is saved under `mcp:<serverName>`. The canonical key is left untouched.

### Token Refresh

When a token expires, ggcode automatically refreshes it using the stored refresh token. The refreshed token is saved only to the server-name key — the canonical key is never overwritten during refresh (it may belong to a different account).

### Multiple Same-URL Servers

You can configure multiple MCP servers pointing to the same URL but authenticated with different accounts:

```yaml
mcp_servers:
  cf:
    name: cf
    type: http
    url: https://mcp.cloudflare.com/mcp
  cf2:
    name: cf2
    type: http
    url: https://mcp.cloudflare.com/mcp
```

Each server registers tools with distinct names (e.g., `mcp__cf__execute` vs `mcp__cf2__execute`) and distinct descriptions (e.g., different embedded account IDs), so the agent can correctly target the right account when you mention a specific server name.
