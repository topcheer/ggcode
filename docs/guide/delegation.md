# Delegation

ggcode can delegate tasks to other AI coding agents running on your system. Each agent runs autonomously with its own API key and billing — no extra configuration needed.

## How It Works

When ggcode detects supported AI agents installed on your system (via `$PATH`), a `delegate` tool becomes available. You can ask ggcode to delegate a task in natural language:

```
> let copilot analyze the authentication flow in this codebase
```

ggcode will invoke the specified agent, which runs in the current working directory with full access to your project.

## Supported Agents

ggcode auto-detects the following agents. Only agents found on your system appear in the `delegate` tool.

| Agent | Binary | Use Case |
|-------|--------|----------|
| **GitHub Copilot** | `copilot` | GitHub workflows, code explanation, refactoring |
| **Claude** (Anthropic) | `claude` | Deep reasoning, complex code generation |
| **Cursor** | `cursor` | Code-aware editing and refactoring |
| **Codex** (OpenAI) | `codex` | Code generation and debugging |
| **Gemini** (Google) | `gemini` | Multimodal analysis, code review |
| **Kimi** (Moonshot) | `kimi` | Long-context code understanding |
| **Qwen** (Alibaba) | `qwen` | Multi-language code generation |
| **Droid** (Factory) | `droid` | Autonomous multi-file refactoring |
| **OpenCode** | `opencode` | Lightweight multi-provider agent |
| **KiloCode** | `kilocode` | Code generation and transformation |
| **Trae** (ByteDance) | `trae` | Real-time coding assistance |
| **Kiro** (AWS) | `kiro` | IDE-integrated development |
| **Qoder** | `qoder` | Code generation and review |
| **Pi** (PolyMind) | `pi` | Specialized analysis |
| **OpenCode** | `opencode` | Open-source multi-provider agent |
| **Fast Agent** | `fast-agent` | MCP-native agent framework |

## Usage

### Natural Language

Simply mention the agent by name:

```
> ask claude to review the security of src/auth/
> let cursor refactor the database layer
> use codex to write tests for internal/handler/
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
