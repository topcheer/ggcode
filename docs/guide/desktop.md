# ggcode Desktop

Native desktop application for macOS, Windows, and Linux with full TUI and GUI features.

## Overview

ggcode Desktop is built with [Wails](https://wails.io) (Go + WebView), bundling the complete TUI experience alongside a graphical interface for configuration and management.

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

- **Session management** — create, switch, and manage coding sessions
- **IM management UI** — configure and monitor IM adapters visually
- **MCP browser** — explore connected MCP servers and their tools
- **Language Servers** — view LSP server detection status and install missing servers with one click (user > global > project scope)
- **Config editor** — edit settings through a structured UI
- **Multiple workspaces** — manage several projects side by side

Keyboard shortcuts are identical to the CLI TUI.

## Auto-Update

ggcode Desktop checks for updates automatically and applies them on restart. No manual download required for subsequent versions.
