# Delegation

ggcode can delegate tasks to other AI coding agents running on your system. Each agent runs autonomously with its own API key and billing — no extra configuration needed.

## How It Works

When ggcode detects supported AI agents installed on your system (via `$PATH`), a `delegate` tool becomes available. You can ask ggcode to delegate a task in natural language:

```
> let copilot analyze the authentication flow in this codebase
```

ggcode will invoke the specified agent, which runs in the current working directory with full access to your project.

## Supported Agents

ggcode auto-detects ACP-compatible agents from the built-in registry
(`internal/acp/discovery.go`). Only agents whose binary is found in `$PATH`
appear in the `delegate` tool — its description always lists the exact set
detected on your system.

| Agent | Binary | Use Case |
|-------|--------|----------|
| **GitHub Copilot** | `copilot` | GitHub workflows, code explanation, refactoring |
| **Droid** (Factory) | `droid` | Autonomous code generation, multi-file refactoring |
| **OpenCode** | `opencode` | Lightweight agent with multi-provider LLM support |

## Usage

### Natural Language

Simply mention the agent by name:

```
> ask copilot to review the security of src/auth/
> let opencode refactor the database layer
> use droid to write tests for internal/handler/
```

### Direct Tool Call

The `delegate` tool accepts:
- **agent** — the agent name (e.g., `copilot`, `claude`)
- **prompt** — the task description with all necessary context
- **description** — optional short label for the live delegate panel

## Async Delegation

Some agents run asynchronously as sub-agents. When this happens, ggcode returns immediately and you can track progress:

- Use `list_agents` to see running delegations
- Use `wait_agent` to wait for a specific delegation to complete
- Results are displayed inline when ready

## Delegation vs A2A

| Feature | `delegate` | `a2a_remote` |
|---------|-----------|-------------|
| Protocol | ACP (Agent Client Protocol) | A2A (Agent-to-Agent) |
| Target | Local AI agents (Copilot, Claude, etc.) | Remote ggcode instances |
| Discovery | Auto-detect from `$PATH` | mDNS + registry |
| Use case | Second opinion, agent-specific capabilities | Cross-project collaboration |

See [A2A Protocol](a2a.md) for delegating to remote ggcode instances.

## Requirements

- The agent must be installed and accessible via `$PATH`
- Each agent uses its own API key — configure them per the agent's documentation
- Agents must support ACP (Agent Client Protocol) mode
