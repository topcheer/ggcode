# CLI Reference

> Run `ggcode --help` to see all available commands and flags for your version.

## Core Commands

### Interactive TUI

```bash
ggcode
```

Starts the interactive terminal UI in the current project directory.

### Version

```bash
ggcode version
ggcode --version
ggcode -v
```

Prints the installed version.

### Pipe Mode (Non-Interactive)

```bash
ggcode -p "fix the failing tests"
cat error.log | ggcode -p "explain this error"
```

Runs a single prompt without launching the TUI. Reads stdin if available.

---

## Session Management

```bash
ggcode --resume <session-id>     # resume a specific session
ggcode --resume-picker            # interactively select a session to resume
```

---

## Flags

| Flag | Description |
|------|-------------|
| `--vendor <name>` | Override the configured vendor (e.g. `openai`) |
| `--allowedTools <tools>` | Restrict available tools in pipe mode |
| `--config <path>` | Override the config file path |
| `--bypass` | Auto-approve mode (skip confirmation prompts) |
| `--output <file>` | Write output to a file |
| `-p, --prompt` | Pipe mode with a prompt string |

---

## Subcommands

### MCP Management

```bash
ggcode mcp install     # install an MCP server
ggcode mcp list        # list configured MCP servers
ggcode mcp uninstall   # remove an MCP server
```

### IM (Instant Messaging) Management

```bash
ggcode im config add     # add an IM adapter
ggcode im config list    # list IM adapters
ggcode im config status  # show IM adapter status
```

### LLM Probe

```bash
ggcode llm-probe
```

Tests connectivity to all configured endpoints and reports results.

### Harness Workflow

```bash
ggcode harness init      # initialize a harness project
ggcode harness queue     # queue tasks
ggcode harness run       # run queued tasks
ggcode harness review    # review results
ggcode harness promote   # promote a build
ggcode harness release   # release a build
```

### Daemon Mode

```bash
ggcode daemon
```

Runs ggcode as a background daemon.

### ACP Mode

```bash
ggcode acp
```

Starts ggcode in ACP (Agent Communication Protocol) mode for programmatic integration.

### Shell Completion

```bash
ggcode completion bash
ggcode completion zsh
ggcode completion fish
ggcode completion powershell
```

Add the output to your shell profile to enable tab-completion for ggcode commands.
