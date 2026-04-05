# ggcode

A terminal-based AI coding agent powered by LLMs. Ask questions about your codebase,
refactor, debug, write new features — all from your terminal.

## Features

- **Multi-Vendor Endpoint Support** — Configure real vendors, plans, regions, and models
- **Protocol Adapters** — OpenAI-compatible, Anthropic-compatible, and Gemini backends
- **Agentic Tool Loop** — The agent reads, writes, edits, searches, and runs commands autonomously
- **MCP Client** — Connect to Model Context Protocol servers for extended tool sets
- **Plugin System** — Load external tool plugins dynamically
- **Session Management** — Save, resume, and export conversations
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

### Go installer

```bash
go install github.com/topcheer/ggcode/cmd/ggcode-installer@latest
ggcode-installer
```

The installer downloads the matching `ggcode` binary from GitHub Releases and places it
into `GOBIN`, the first `GOPATH/bin`, or `~/go/bin`.

### npm

```bash
npm install -g @topcheer/ggcode
```

The npm package is a thin wrapper that downloads the platform binary from GitHub Releases
during install or on first run.

### pip

```bash
pip install ggcode
```

The Python package installs a small launcher that downloads the matching GitHub Release
binary on first run.

### Clone & build from source

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
make install  # install ggcode from source into your Go bin dir
make install-installer  # install the Go release installer
make clean    # Remove build artifacts
```

## Quick Start

1. Set your API key:

```bash
export ZAI_API_KEY="your-zai-key"
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

Create `~/.ggcode/ggcode.yaml` (the default path), or keep a project-local file and pass it with `--config ./ggcode.yaml`:

```yaml
vendor: zai
endpoint: cn-coding-openai
model: glm-5-turbo
language: en
default_mode: supervised

vendors:
  zai:
    display_name: Z.ai
    api_key: ${ZAI_API_KEY}
    endpoints:
      cn-coding-openai:
        display_name: CN Coding Plan
        protocol: openai
        base_url: https://open.bigmodel.cn/api/coding/paas/v4
        default_model: glm-5-turbo
        selected_model: glm-5-turbo
        max_tokens: 8192
        models: [glm-5-turbo, glm-5-plus]
      cn-coding-anthropic:
        display_name: CN Coding Plan (Anthropic)
        protocol: anthropic
        base_url: https://open.bigmodel.cn/api/anthropic
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
| `/provider [vendor]` | Open the provider manager and switch vendor/endpoint/model |
| `/mode <mode>` | Switch runtime mode (`supervised`, `plan`, `auto`, `bypass`, `autopilot`) |
| `/lang <code>` | Switch interface language (`en` or `zh-CN`) |
| `/sessions` | List saved sessions |
| `/resume <id>` | Resume a previous session |
| `/export <id>` | Export session to markdown |
| `/clear` | Clear conversation history |
| `/mcp` | Show MCP servers and tools |
| `/plugins` | List loaded plugins |
| `/allow <tool>` | Always allow a tool |
| `/exit`, `/quit` | Exit ggcode |

## Release-backed installers

All end-user installers use GitHub Releases as the binary source of truth.

- **Go installer**: source-installed wrapper that downloads the release binary into your Go bin dir
- **npm package**: thin JavaScript wrapper in [`npm/`](npm/)
- **pip package**: thin Python wrapper in [`python/`](python/)

Each installer resolves the current OS/arch, downloads the matching release archive, verifies
it against `checksums.txt`, extracts `ggcode`, and reuses the cached binary on later runs.

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
