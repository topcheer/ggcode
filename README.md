# ggcode

**ggcode** is an AI coding agent for the terminal. It can understand a codebase, edit files, run commands, manage checkpoints, connect to MCP tools, and keep working inside a polished TUI instead of bouncing between scripts and browser tabs.

If you want a terminal-native coding workflow that feels like a product, not a demo, this is what ggcode is for.

## Why people use ggcode

- **Stay in the terminal** — chat, inspect code, edit files, review diffs, and manage sessions in one place
- **Work with real coding plans and endpoints** — OpenAI-compatible, Anthropic-compatible, Gemini, and multiple coding-oriented vendor presets
- **Keep control when it matters** — supervised, plan, auto, bypass, and autopilot modes let you choose how much the agent can do
- **Recover quickly** — undo file changes with checkpoints instead of manually repairing bad edits
- **Scale up when needed** — use MCP tools, plugins, skills, memory, background commands, and sub-agents
- **Fit daily usage** — bilingual UI, resumable sessions, queueing while the agent is busy, and shell-friendly install flows

## Installation

### Go installer

```bash
go install github.com/topcheer/ggcode/cmd/ggcode-installer@latest
ggcode-installer
```

The installer downloads the matching GitHub Release binary into `GOBIN`, the first `GOPATH/bin`, or `~/go/bin`.

### npm

```bash
npm install -g @ggcode-cli/ggcode
```

The npm wrapper downloads the latest ggcode GitHub Release by default. Set `GGCODE_INSTALL_VERSION`
if you need to pin a specific release.

### pip

```bash
pip install ggcode
```

The Python wrapper also downloads the latest ggcode GitHub Release by default and respects
`GGCODE_INSTALL_VERSION` for explicit pinning.

### Build from source

```bash
git clone https://github.com/topcheer/ggcode.git
cd ggcode
go build -o ggcode ./cmd/ggcode
./ggcode
```

### Platform notes

- **macOS / Linux** command execution uses `sh`
- **Windows** command execution prefers **Git Bash** and falls back to **PowerShell**
- Shell completions are available for **bash**, **zsh**, **fish**, and **PowerShell**

## Quick start

### 1. Set up a model endpoint

The simplest path is still setting a normal vendor API key:

```bash
export ZAI_API_KEY="your-key"
# or OPENAI_API_KEY / ANTHROPIC_API_KEY / GEMINI_API_KEY / OPENROUTER_API_KEY / ...
```

If you use an **Anthropic-compatible endpoint**, ggcode can also bootstrap it on first launch from:

```bash
export ANTHROPIC_BASE_URL="https://your-endpoint"
export ANTHROPIC_AUTH_TOKEN="your-token"
```

### 2. Start ggcode

```bash
ggcode
```

On first launch, ggcode asks you to choose your preferred UI language.

### 3. Start with a real task

Examples:

```text
Explain how this project is structured
Refactor the auth middleware to use JWT
Add tests for the session store
Find why startup feels slow in the TUI
```

### 4. Use the built-in workflow features

- **`Ctrl+C`** cancels the active run
- If the agent is busy, you can keep typing — new prompts are **queued**
- **`/undo`** reverts the last file edit
- **`/provider`** switches vendor / endpoint / model
- **`/mode`** changes how much autonomy the agent gets
- **`/mcp`** shows connected MCP servers and their tools

## What ggcode can do

From the product point of view, ggcode is more than “chat with a model”:

- **Code understanding** — read files, search the repo, inspect git status and diffs
- **Code changes** — create files, edit targeted regions, and checkpoint edits for undo
- **Command execution** — run foreground commands or long-running background jobs
- **Parallel help** — spawn sub-agents and inspect their progress
- **Memory and context** — load project memory files like `GGCODE.md`, `AGENTS.md`, `CLAUDE.md`, and `COPILOT.md`
- **Extensibility** — connect MCP servers, custom plugins, and skills
- **Session continuity** — save, resume, export, and compact conversations

## Modes: how much freedom the agent gets

| Mode | Best for | What it means |
| --- | --- | --- |
| `supervised` | Most users | Ask when a tool is not explicitly allowed or denied |
| `plan` | Safe exploration | Read-only style investigation; blocks writes and command execution |
| `auto` | Faster routine work | Automatically proceed on safer actions, stay cautious on risky ones |
| `bypass` | High-trust workflows | Allow almost everything, only stopping on critical operations |
| `autopilot` | Power users | Like bypass, but also keeps going when the model would normally stop to ask |

## Slash commands you will actually use

### Core workflow

| Command | What it does |
| --- | --- |
| `/help` or `/?` | Show the in-app help |
| `/provider [vendor]` | Open the provider manager and switch vendor / endpoint / model |
| `/model <name>` | Switch model directly |
| `/mode <mode>` | Change permission mode |
| `/status` | Show current status |
| `/config` | View or update configuration |
| `/lang <en|zh-CN>` | Change interface language |

### Session and recovery

| Command | What it does |
| --- | --- |
| `/sessions` | List saved sessions |
| `/resume <id>` | Resume a previous session |
| `/export <id>` | Export a session to Markdown |
| `/clear` | Clear the current conversation |
| `/compact` | Compress conversation history |
| `/undo` | Revert the last file edit |
| `/checkpoints` | List available edit checkpoints |

### Extended capabilities

| Command | What it does |
| --- | --- |
| `/mcp` | Inspect MCP servers and MCP tools |
| `/plugins` | List loaded plugins |
| `/skills` | Browse available skills |
| `/memory` | Inspect stored memory |
| `/agents` | List active sub-agents |
| `/agent <id>` | Inspect a sub-agent |
| `/todo` | View or manage todo state |
| `/image` | Attach an image |
| `/bug` | Report a bug |
| `/init` | Generate `GGCODE.md` for the current project |
| `/fullscreen` | Toggle fullscreen mode |
| `/exit`, `/quit` | Exit ggcode |

## Non-interactive and scripted usage

ggcode also supports a simple pipe-mode workflow when you do not want to open the TUI:

```bash
ggcode \
  --prompt "Summarize the changes in this repository" \
  --allowedTools read_file \
  --output summary.md
```

Useful flags:

- `--prompt` / `-p` — run a non-interactive prompt
- `--allowedTools` — restrict which tools are allowed in pipe mode
- `--output` — write the answer to a file instead of stdout
- `--bypass` — start in bypass mode
- `--resume <id>` — resume a previous session immediately
- `--config <path>` — use a specific config file

## Configuration

Most users only need a small config file:

```yaml
vendor: zai
endpoint: cn-coding-openai
model: glm-5-turbo
language: en
default_mode: supervised

allowed_dirs:
  - .

tool_permissions:
  read_file: allow
  search_files: allow
  run_command: ask
  write_file: ask
```

ggcode ships with built-in presets for mainstream vendors and several coding-oriented endpoints, so you usually start by choosing a vendor or setting API keys rather than writing the full provider catalog yourself.

For the complete reference, examples, vendor catalog, hooks, MCP servers, plugins, and sub-agent settings, see:

- [`ggcode.example.yaml`](ggcode.example.yaml)

## MCP, plugins, hooks, and memory

### MCP servers

Use MCP when you want ggcode to access external tool ecosystems.

```yaml
mcp_servers:
  - name: filesystem
    command: npx
    args:
      - -y
      - "@anthropic/mcp-filesystem"
      - /path/to/allowed/dir
```

ggcode discovers MCP tools automatically and makes them available in the agent loop.

### Plugins and skills

- **Plugins** add custom tools from config
- **Skills** add higher-level capabilities and workflows
- **`/skills`** is the easiest place to see what is currently available

### Project memory

ggcode can load project guidance from files such as:

- `GGCODE.md`
- `AGENTS.md`
- `CLAUDE.md`
- `COPILOT.md`

Use these files to tell ggcode how your project works, what conventions to follow, and what to avoid.

## Shell completions

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

## More documentation

- **Want to use the product?** Start here in the README
- **Want the full config surface?** See [`ggcode.example.yaml`](ggcode.example.yaml)
- **Want implementation details?** See [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)

## License

MIT
