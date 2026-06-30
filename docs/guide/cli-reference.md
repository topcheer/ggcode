# CLI Reference

> Run `ggcode --help` to see all available commands and flags for your version.

## Core Commands

### Interactive TUI (default)

```bash
ggcode                    # Launch interactive TUI
ggcode --bypass           # Start in bypass permission mode
ggcode --config <path>    # Use a specific config file
```

### Pipe Mode

```bash
ggcode -p "prompt"        # Non-interactive: send prompt, print response
echo "fix typo" | ggcode  # Read from stdin
```

### Resume Session

```bash
ggcode --resume <id>      # Resume a specific session
ggcode --resume           # Auto-resume latest session
ggcode --resume-picker    # Open session picker
```

### Daemon Mode

```bash
ggcode daemon             # Start daemon (headless + IM gateway)
ggcode daemon --follow    # Daemon with terminal follow display
ggcode daemon --bypass    # Daemon in bypass mode
ggcode daemon --background  # Fork to background
```

## Subcommands

### harness

Harness-engineering workflow for structured multi-step tasks:

```bash
ggcode harness create <title>          # Create a task
ggcode harness list                    # List tasks
ggcode harness show <id>               # Show task details
ggcode harness start <id>              # Start working on a task
ggcode harness monitor                 # Monitor active work
```

### mcp

MCP server management:

```bash
ggcode mcp list                        # List configured MCP servers
ggcode mcp add <name> <command>        # Add an MCP server
ggcode mcp remove <name>               # Remove an MCP server
```

### plugin

Manage gRPC and command plugins:

```bash
ggcode plugin list                     # List configured plugins
ggcode plugin install <name> <cmd...>  # Install a plugin (--env K=V, --type grpc|command)
ggcode plugin uninstall <name>         # Remove a plugin
ggcode plugin test <name>              # Test a plugin can start and handshake
```

See [gRPC Plugins](grpc-plugins.md) for the full guide.

### im

IM integration management:

```bash
ggcode im list                         # List configured IM adapters
```

### acp

Agent Client Protocol support for editor integration (JetBrains, Zed, etc.):

```bash
ggcode acp                             # Start ACP server
```

### llm-probe

Test LLM provider connectivity and list available models:

```bash
ggcode llm-probe                       # Test current provider
```

### Status

```bash
ggcode status              # List all running instances
ggcode status list         # Same as above (explicit)
ggcode status list --agent # Show only agent busy/idle status
ggcode status list --im    # Show only IM adapter status
ggcode status list --mobile # Show only mobile tunnel connections
ggcode status list --json  # JSON output for scripting
ggcode status get [workspace] # Detailed status for a specific workspace
```

The status command reads port files from `~/.ggcode/run/<sessionID>.json`. Each running
ggcode instance (TUI, daemon, desktop) writes its own port file keyed by session ID.
Multiple instances in the same workspace each appear as separate entries.

| Column | Description |
|--------|-------------|
| PID | OS process ID |
| WORKSPACE | Working directory |
| SESSION | Session ID (truncated) |
| MODE | Permission mode |
| AGENT | `busy` or `idle` |
| IM | Number of IM adapters (online count in parentheses) |
| MOBILE | Mobile tunnel connection status |
| MODEL | Active LLM model |

Stale port files (from crashed or killed processes) are automatically cleaned up on read.

### completion

Generate shell completion scripts:

```bash
ggcode completion bash                 # Bash completion
ggcode completion zsh                  # Zsh completion
ggcode completion fish                 # Fish completion
ggcode completion powershell           # PowerShell completion
```

### version

```bash
ggcode version                         # Print version, commit, and build date
```

## Global Flags

| Flag | Description |
|------|-------------|
| `--config <path>` | Use a specific config file |
| `--bypass` | Start in bypass permission mode |
| `-p, --pipe <prompt>` | Non-interactive pipe mode |
| `--resume [id]` | Resume a session |
| `--resume-picker` | Open session picker |
| `-h, --help` | Show help |
