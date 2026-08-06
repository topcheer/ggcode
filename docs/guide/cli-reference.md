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

Additional pipe-mode flags:
- `--allowedTools <name>` — restrict tools (repeatable)
- `--no-harness` — skip harness auto-run routing, force normal agent
- `--output <path>` — write output to file (default: stdout)

### Resume Session

```bash
ggcode --resume <id>      # Resume a specific session
ggcode --resume           # Auto-resume latest session
ggcode --resume-picker    # Open session picker
ggcode --new-session      # Skip auto-resume, always start fresh
```

`--resume`, `--resume-picker`, and `--new-session` are mutually exclusive.

### Daemon Mode

```bash
ggcode daemon             # Start daemon (headless + IM gateway + lanchat)
ggcode daemon --follow    # Daemon with terminal follow display
ggcode daemon --bypass    # Daemon in bypass mode
ggcode daemon --background  # Fork to background
ggcode daemon --tunnel    # Start with mobile tunnel (QR code for GGCode Mobile)
ggcode daemon --new-session  # Skip auto-loading most recent session
```

The daemon automatically loads the most recent unlocked session on startup. Use `--new-session` to always start fresh. The daemon also initializes a lanchat Hub for LAN discovery and messaging.

## Subcommands

### harness

Harness-engineering workflow for structured multi-step tasks. Tasks run in isolated git worktrees with automated checks, review gates, and progressive release rollouts.

> See [Harness Workflow](harness.md) for the complete guide.

```bash
ggcode harness init                    # Initialize harness scaffolding
ggcode harness queue <goal>            # Queue a task to the backlog
ggcode harness run [goal]              # Execute a task (or drain queue)
ggcode harness rerun <task-id>         # Rerun a single failed task
ggcode harness tasks                   # List all tasks and state
ggcode harness contexts                # Summarize state by bounded context
ggcode harness check                   # Run structural validation
ggcode harness doctor                  # Inspect harness health
ggcode harness gc                      # Archive stale runs, prune logs
```

**Review & Promote:**

```bash
ggcode harness review                  # List tasks awaiting review
ggcode harness review approve <id>     # Approve a completed task
ggcode harness review reject <id>      # Reject back into retry flow
ggcode harness inbox                   # Show actionable work for owners
ggcode harness inbox promote --owner <name>  # Batch-promote approved tasks
ggcode harness inbox retry --owner <name>    # Batch-retry failed tasks
ggcode harness promote                 # List promotable tasks
ggcode harness promote apply <id>      # Promote an approved task to main
ggcode harness promote apply --all-approved  # Promote all approved
```

**Release (progressive delivery):**

```bash
ggcode harness release                 # Show release plan
ggcode harness release apply           # Batch promoted tasks into a release
ggcode harness release rollouts        # List wave rollouts
ggcode harness release advance <id>    # Advance a rollout to next wave
ggcode harness release pause <id>      # Pause active wave
ggcode harness release resume <id>     # Resume a paused rollout
ggcode harness release abort <id>      # Abort remaining waves
ggcode harness release approve <id>    # Approve a planned wave
ggcode harness release reject <id>     # Reject a planned wave
```

**Monitor:**

```bash
ggcode harness monitor                 # Show persisted activity
ggcode harness monitor --watch         # Live refresh until interrupted
ggcode harness monitor --interval 5s   # Custom refresh interval
```

### mcp

MCP (Model Context Protocol) server management:

```bash
ggcode mcp list                        # List configured MCP servers
ggcode mcp install                     # Interactive MCP setup wizard
ggcode mcp install <name> <command...> # Install a stdio MCP server
ggcode mcp install <name> -t http <url>  # Install an HTTP MCP server
ggcode mcp install <name> -t ws <url>  # Install a WebSocket MCP server
ggcode mcp uninstall <name>            # Remove an MCP server
```

Install options:
- `-t, --type <stdio|http|ws>` — transport type (default: stdio)
- `-e, --env KEY=VALUE` — environment variables (repeatable)
- `--header KEY:VALUE` — HTTP headers for http/ws transports (repeatable)

> See [MCP Guide](mcp.md) for the full guide.

### plugin

Manage gRPC and command plugins:

```bash
ggcode plugin list                     # List configured plugins
ggcode plugin install <name> <cmd...>  # Install a plugin
ggcode plugin uninstall <name>         # Remove a plugin
ggcode plugin test <name>              # Test a plugin can start and handshake
```

Install options:
- `-e, --env KEY=VALUE` — environment variables (repeatable)
- `--type grpc|command` — plugin type (default: grpc)

> See [gRPC Plugins](grpc-plugins.md) for the full guide.

### im

IM (Instant Messaging) adapter and binding management:

```bash
ggcode im status                       # Show overview of adapters + bindings
ggcode im list                         # List configured IM adapters
ggcode im list --json                  # JSON output for scripting
ggcode im bindings                     # List channel bindings
ggcode im bind <adapter>               # Bind a channel to an adapter
ggcode im unbind [<adapter>]           # Unbind a channel
ggcode im pair <adapter>               # Start interactive pairing
ggcode im share                        # Generate a PrivateClaw share link
```

Binding options:
- `--channel <id>` — target channel/thread ID
- `--thread <id>` — thread ID (platform-specific)
- `--target <name>` — target name for binding
- `--workspace <path>` — workspace path (default: current directory)

IM adapter configuration:

```bash
ggcode im config add [name]            # Add an adapter configuration
ggcode im config remove <name>         # Remove an adapter configuration
ggcode im config show <name>           # Show adapter details
ggcode im config show <name> --json    # JSON output
ggcode im config set <name> <key> <val>  # Modify a single setting
```

> See [IM Integration](im-integration.md) for the full guide.

### acp

Agent Client Protocol support for editor integration (JetBrains, Zed, VS Code, etc.):

```bash
ggcode acp                             # Start ACP server (stdio JSON-RPC)
ggcode acp --vendor openai             # Override vendor
ggcode acp --endpoint <name>           # Override endpoint
ggcode acp --model <name>              # Override model
```

> See [ACP Guide](acp.md) for the full guide.

### llm-probe

Test LLM provider connectivity, authentication, and token usage accuracy:

```bash
ggcode llm-probe                       # Test all configured endpoints
ggcode llm-probe --vendor zai          # Test only a specific vendor
ggcode llm-probe --endpoint <name>     # Test only a specific endpoint
ggcode llm-probe --model <name>        # Override model for all endpoints
ggcode llm-probe --list-models         # List models (no API call tests)
ggcode llm-probe --timeout 30          # 30s timeout per API call
ggcode llm-probe -v                    # Verbose: full request/response
```

### status

Discover running ggcode instances and query their runtime state:

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

### report

Generate a self-contained HTML analytics report from all session JSONL files:

```bash
ggcode report                           # Generate report and open in browser
ggcode report -o ~/Desktop/report.html  # Specify output path
ggcode report --no-open                 # Generate without opening browser
ggcode report --sessions-dir /custom    # Override sessions directory
```

The report includes:
- **Overview**: daily token usage trends, workspace distribution, tool call summary, date range filter
- **Sessions**: sortable table with workspace/date filters, click-through to detail
- **Session Detail**: per-turn token bars, TTFT model comparison, draggable time range slider
- **Daily Details**: per-model token breakdown for any selected day (click daily chart bars)
- **Performance**: TTFT/duration histograms (P50/P95/P99), tool success rates

Charts use embedded Chart.js — fully offline, no CDN dependencies. The generated HTML
file is self-contained and shareable.

### completion

Generate shell completion scripts:

```bash
ggcode completion bash                 # Bash completion
ggcode completion zsh                  # Zsh completion
ggcode completion fish                 # Fish completion
ggcode completion powershell           # PowerShell completion
```

> See [Shell Completion](shell-completion.md) for installation instructions.

### version

```bash
ggcode version                         # Print version, commit, and build date
```

## Global Flags

| Flag | Description |
|------|-------------|
| `--config <path>` | Use a specific config file |
| `--bypass` | Start in bypass permission mode |
| `-p, --prompt <prompt>` | Non-interactive pipe mode |
| `--allowedTools <name>` | Restrict tools in pipe mode (repeatable) |
| `--no-harness` | Skip harness auto-run routing in pipe mode |
| `--output <path>` | Output file path (default: stdout) |
| `--resume [id]` | Resume a session |
| `--resume-picker` | Open session picker |
| `--new-session` | Skip auto-resume, always start a new session |
| `-v` | Shorthand for `--version` |
| `-h, --help` | Show help |
