# ggcode

A terminal-based AI coding agent powered by LLMs. Ask questions about your codebase,
refactor, debug, write new features — all from your terminal.

## Features

- **Multi-Provider Support** — Anthropic, OpenAI GPT, Google Gemini
- **Custom Base URLs** — Use any OpenAI-compatible endpoint (local models, proxies)
- **Agentic Tool Loop** — The agent reads, writes, edits, searches, and runs commands autonomously
- **MCP Client** — Connect to Model Context Protocol servers for extended tool sets
- **Plugin System** — Load external tool plugins dynamically
- **Session Management** — Save, resume, and export conversations
- **Cost Tracking** — Real-time token usage and cost estimation
- **Permission System** — Fine-grained control over which tools and commands are allowed
- **Rich TUI** — Bubble Tea terminal UI with markdown rendering and syntax highlighting
- **Bilingual TUI** — English by default, switch to Simplified Chinese with `/lang zh-CN`
- **Environment Variable Expansion** — API keys via `${ENV_VAR}` in config, no plaintext secrets

## Built-in Tools

| Tool | Description |
|------|-------------|
| `read_file` | Read file contents |
| `write_file` | Create or overwrite files |
| `edit_file` | Apply targeted text edits |
| `list_directory` | List directory contents |
| `search_files` | Search file contents with patterns |
| `glob` | Find files by glob pattern |
| `run_command` | Execute shell commands |
| `git_status` | Show git working tree status |
| `git_diff` | Show git diffs |
| `git_log` | Show git commit history |

## Installation

### Go Install

```bash
go install github.com/topcheer/ggcode/cmd/ggcode@latest
```

### Clone & Build

```bash
git clone https://github.com/topcheer/ggcode.git
cd ggcode
go build -o ggcode ./cmd/ggcode
./ggcode
```

### Makefile

```bash
make build    # Build binary to bin/ggcode
make test     # Run all tests
make lint     # Run go vet
make install  # go install
make clean    # Remove build artifacts
```

## Quick Start

1. Set your API key:

```bash
export ANTHROPIC_API_KEY="sk-ant-..."
```

2. Run ggcode:

```bash
ggcode
```

3. Start chatting:

```
> Explain the structure of this project
> Refactor the auth middleware to use JWT
> Write unit tests for the user service
```

## Configuration

Create a `ggcode.yaml` in the current directory or `~/.ggcode/ggcode.yaml`:

```yaml
# Provider: anthropic, openai, or gemini
provider: anthropic
model: claude-sonnet-4-20250514
language: en

providers:
  anthropic:
    api_key: ${ANTHROPIC_API_KEY}
    max_tokens: 8192
  openai:
    api_key: ${OPENAI_API_KEY}
    base_url: https://api.openai.com/v1  # optional custom endpoint
    max_tokens: 8192
  gemini:
    api_key: ${GEMINI_API_KEY}
    max_tokens: 8192

# Restrict file access to these directories
allowed_dirs:
  - .

# Max agentic loop iterations per turn
max_iterations: 50

# Tool-level permissions: ask, allow, deny
tool_permissions:
  read_file: allow
  search_files: allow
  run_command: ask
  write_file: ask
```

See [ggcode.example.yaml](ggcode.example.yaml) for the full example.

## TUI Language and Controls

- Default UI language is English.
- Switch the current session to Simplified Chinese with `/lang zh-CN`.
- Switch back with `/lang en`.
- Persist the preferred UI language with `language: en` or `language: zh-CN` in config.
- `Ctrl+C` cancels the active run. When idle, the first `Ctrl+C` clears the input and arms exit confirmation; press `Ctrl+C` again to quit.
- While a run is active, you can keep typing and submit more prompts. They queue and are sent automatically after the current loop finishes.

## Slash Commands

| Command | Description |
|---------|-------------|
| `/help` | Show help message |
| `/model <name>` | Switch model |
| `/provider <name>` | Switch provider |
| `/lang <code>` | Switch interface language (`en` or `zh-CN`) |
| `/cost` | Show session cost stats |
| `/cost all` | Show all session costs |
| `/sessions` | List saved sessions |
| `/resume <id>` | Resume a previous session |
| `/export <id>` | Export session to markdown |
| `/clear` | Clear conversation history |
| `/mcp` | Show MCP servers and tools |
| `/plugins` | List loaded plugins |
| `/allow <tool>` | Always allow a tool |
| `/exit`, `/quit` | Exit ggcode |

### Keyboard Shortcuts

- **↑/↓** — Browse command history
- **Ctrl+C** — Cancel active work, otherwise clear input then press again to exit
- **Ctrl+D** — Exit

## MCP Server Configuration

Connect to MCP servers for extended tool sets:

```yaml
mcp_servers:
  - name: filesystem
    command: npx
    args:
      - -y
      - "@anthropic/mcp-filesystem"
      - /path/to/allowed/dir
    env:
      NODE_ENV: production
```

The agent will automatically discover and use tools exposed by connected MCP servers.

## Plugin System

Load external tool plugins from config:

```yaml
plugins:
  - name: my-tools
    type: command
    commands:
      - name: deploy
        description: Deploy the application
        execute: ./scripts/deploy.sh
      - name: lint_check
        description: Run custom linter
        execute: npm
        args: [run, lint]
```

Use `/plugins` in the REPL to see loaded plugins and their tools.

## Shell Completions

ggcode uses Cobra and supports bash, zsh, fish, and PowerShell completions:

```bash
# Bash
ggcode completion bash > /etc/bash_completion.d/ggcode

# Zsh
ggcode completion zsh > "${fpath[1]}/_ggcode"

# Fish
ggcode completion fish > ~/.config/fish/completions/ggcode.fish

# PowerShell
ggcode completion powershell | Out-String | Invoke-Expression
```

## Screenshots

<!-- TODO: Add screenshots/GIF here -->

## License

MIT
