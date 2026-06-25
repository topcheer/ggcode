# ggcode Desktop

Native desktop application for macOS, Windows, and Linux.

## Overview

ggcode Desktop is built with [Wails](https://wails.io) (Go backend + React/WebView frontend). It provides a graphical interface for chat, configuration, and management alongside the same agent engine that powers the CLI.

## Installation

### macOS

Download the universal `.dmg` from [GitHub Releases](https://github.com/topcheer/ggcode/releases) or [ggcode.dev](https://ggcode.dev).

### Windows

```powershell
winget install gg.ai.ggcode-desktop
```

Or download the `.msi` installer (per-user or per-machine) from GitHub Releases.

### Linux

Download from GitHub Releases:

| Format | File |
|--------|------|
| Debian/Ubuntu | `.deb` |
| Fedora/RHEL | `.rpm` |
| Universal | `.AppImage` |

## Features

### Core
- **Chat interface** — visual chat with streaming responses, tool call visualization, and reasoning display
- **Session management** — create, switch, filter by workspace, and manage coding sessions
- **Multiple workspaces** — manage several projects side by side

### Settings & Configuration
- **Config editor** — edit vendor, endpoint, model, API key, and permission settings through a structured UI
- **Provider picker** — switch between configured LLM providers visually
- **Permission modes** — switch between supervised, plan, auto, bypass, and autopilot

### Integrations
- **MCP browser** — explore connected MCP servers, their tools, prompts, and resources
- **Language Servers (LSP)** — view detection status and install missing servers with one click (scope: user > global > project)
- **IM management** — configure, enable, disable, mute, and monitor IM adapters (QQ, Telegram, Discord, Slack, DingTalk, Feishu, etc.)

### Collaboration
- **LAN Chat** — real-time messaging with other ggcode instances on your local network. Multi-room view with broadcast + DM rooms, unread badges, file attachments, and @agent message approvals. See [LAN Chat](./lan-chat.md).
- **Tunnel sharing** — share sessions to mobile devices via relay
- **Team board** — manage swarm teammates and task assignments
- **Sub-agent panels** — observe running sub-agents and their output

### Power Features
- **File browser** — fullscreen file browser with side-by-side preview
- **Command palette** — quick access to commands
- **Debug console** — view debug logs
- **Context panel** — inspect conversation context and token usage
- **Onboarding wizard** — guided setup for first-time users

## Auto-Update

ggcode Desktop checks for updates automatically and applies them on restart. No manual download required for subsequent versions.

## Keyboard Shortcuts

Common TUI keyboard shortcuts work in the desktop app's chat input. See [Slash Commands](./slash-commands.md) for the full list.
