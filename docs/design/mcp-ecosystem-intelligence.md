# MCP Ecosystem Intelligence

## Overview

The MCP Ecosystem Intelligence feature provides proactive, zero-LLM-cost detection of MCP server issues at the start of each agent session. It surfaces actionable intelligence about the health and configuration of connected MCP servers.

## What It Detects

### 1. Failed/Disconnected Servers
When an MCP server fails to connect, the agent receives a one-time notification with:
- Server name and specific error message
- Actionable debugging tip (check config, connectivity)

### 2. Tool Name Conflicts
When multiple MCP servers expose tools with the same underlying name (e.g., both `server-a` and `server-b` provide a `search` tool), the agent is warned that only the first registered tool will be available.

### 3. Empty Servers (Connected but No Capabilities)
When a server connects successfully but exposes zero tools, prompts, and resources, the agent is notified that the server may be misconfigured.

### 4. Authentication Issues
When a server's error message indicates OAuth/authentication problems (expired tokens, unauthorized access), the agent receives guidance to re-authenticate.

## Implementation

- **File**: `internal/agent/mcp_ecosystem.go`
- **Pattern**: Follows the existing agent gate pattern (like `monorepoScoper`, `toolThermal`, `bgOrphan`)
- **Fires**: Once per session, at iteration 2+ (allowing MCP servers time to connect)
- **Cost**: Zero LLM cost - pure deterministic pattern matching
- **Integration**: Wired via `Agent.SetMCPRuntime()` in `agentruntime/interactive_core.go`

## Competitive Analysis

| Feature | ggcode | Claude Code | Cursor | Cline |
|---------|--------|-------------|--------|-------|
| Server health detection | Yes (proactive gate) | Passive (error display) | No | No |
| Tool conflict detection | Yes (cross-server) | No | No | No |
| Empty server detection | Yes | No | No | No |
| Auth issue detection | Yes (error pattern matching) | Manual | No | No |
| Actionable guidance | Yes (tips included) | Limited | No | No |

## Configuration

No configuration needed. The feature activates automatically when MCP servers are configured. It's non-blocking — the agent can proceed regardless of detected issues.

## Testing

12 unit tests in `internal/agent/mcp_ecosystem_test.go` covering:
- Healthy ecosystem (no false positives)
- Failed server detection
- OAuth issue detection
- Empty server detection
- Tool conflict detection (including disconnected server exclusion)
- Tool name extraction edge cases
- Fire-once behavior
- Nil runtime safety
