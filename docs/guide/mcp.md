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

For MCP servers that require authentication (e.g., remote HTTP servers), ggcode handles the OAuth flow automatically. Tokens are cached in `~/.ggcode/`.
