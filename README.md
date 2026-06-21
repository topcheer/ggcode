# ggcode

<p align="center">
  <img src="ggcode_cli_banner_1775456774280.png" alt="ggcode" width="600" />
</p>

**English** | **[中文](README_zh-CN.md)**

An AI coding agent for the terminal. Understands your codebase, edits files, runs commands, and ships code — with a polished TUI, resumable sessions, and multi-agent support.

---

## Install

**macOS / Linux:**
```bash
curl -fsSL https://ggcode.dev/install.sh | bash
```

**Windows:**
```powershell
irm https://ggcode.dev/install.ps1 | iex
```

**Other methods:** [Homebrew](docs/guide/install.md#macos) · [winget](docs/guide/install.md#windows) · [npm](docs/guide/install.md#npm) · [pip](docs/guide/install.md#pip) · [build from source](docs/guide/install.md#build-from-source)

> All install scripts default to non-privileged (no sudo / admin required).

## Quick Start

```bash
# 1. Run ggcode in your project
cd your-project
ggcode

# 2. On first launch, configure your API key (interactive prompt)
#    Or set it directly:
#    OpenAI:      ggcode --vendor openai
#    Anthropic:   ggcode --vendor anthropic

# 3. Start coding — just type your request
```

New to ggcode? Read the **[Getting Started guide](docs/guide/getting-started.md)**.

## Features

- **Codebase-aware** — reads, understands, and edits your entire project
- **Full dev toolkit** — file edits, shell commands, Git, LSP, search
- **MCP integration** — connect external tools and data sources
- **Multi-agent** — spawn parallel workers, delegate to teammates, A2A protocol
- **Editor integration** — JetBrains, Zed, and ACP-compatible editors via ACP
- **WebUI** — built-in web interface accessible from any browser
- **IM integration** — control from QQ, Telegram, Discord, Slack, Feishu, DingTalk
- **Harness workflow** — isolated task execution with review and promotion
- **Scheduled tasks** — cron jobs, reminders, and background automation
- **Resumable sessions** — pause and resume any conversation
- **Desktop + Mobile** — native apps for macOS, Windows, Linux, iOS, Android

## Platforms

| Platform | Install |
|----------|---------|
| **CLI** (macOS/Linux/Windows) | This repo — the primary interface |
| **[Desktop](docs/guide/desktop.md)** | Native app for macOS, Windows, Linux |
| **[Mobile](docs/guide/mobile.md)** | iOS (TestFlight) and Android (Google Play) |

## Documentation

| Guide | Description |
|-------|-------------|
| [Getting Started](docs/guide/getting-started.md) | First steps, API key setup, basic usage |
| [Installation](docs/guide/install.md) | All install methods for every platform |
| [CLI Reference](docs/guide/cli-reference.md) | Commands, flags, pipe mode |
| [Providers](docs/guide/providers.md) | LLM vendor and endpoint configuration |
| [Slash Commands](docs/guide/slash-commands.md) | In-TUI command reference |
| [Permission Modes](docs/guide/modes.md) | Default, bypass, readonly, yolo |
| [MCP Servers](docs/guide/mcp.md) | Connect external tools via MCP |
| [IM Integration](docs/guide/im-integration.md) | QQ, Telegram, Discord, Slack, Feishu, DingTalk |
| [Harness](docs/guide/harness.md) | Isolated task workflow with review |
| [A2A Protocol](docs/guide/a2a.md) | Cross-instance agent delegation |
| [ACP / Editor](docs/guide/acp.md) | JetBrains, Zed, and ACP-compatible editors |
| [Multi-Agent](docs/guide/multi-agent-modes.md) | Sub-agents, teammates, and team coordination |
| [Configuration](docs/guide/configuration.md) | Full config file reference |
| [Project Memory](docs/guide/project-memory.md) | GGCODE.md, AGENTS.md, CLAUDE.md |
| [Skills](docs/guide/skills.md) | Reusable workflow patterns |
| [Shell Completion](docs/guide/shell-completion.md) | bash, zsh, fish, powershell |

## Quick Links

- **[Download Desktop App](https://ggcode.dev)** · **[Releases](https://github.com/topcheer/ggcode/releases)**
- **[Report a Bug](https://github.com/topcheer/ggcode/issues)** · **[Request a Feature](https://github.com/topcheer/ggcode/issues)**

## License

MIT
