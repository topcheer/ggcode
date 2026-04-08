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

### Native package files from GitHub Releases

Each tagged release now also publishes native installer/package files across desktop and Linux:

| Platform | Release asset | Install example |
| --- | --- | --- |
| macOS | `.pkg` | `sudo installer -pkg ./ggcode_<version>_darwin_universal.pkg -target /` |
| Windows | `.msi` | `msiexec /i .\ggcode_<version>_windows_x64.msi` |
| Debian / Ubuntu | `.deb` | `sudo dpkg -i ./ggcode_<version>_linux_<arch>.deb` |
| Fedora / RHEL / openSUSE | `.rpm` | `sudo rpm -i ./ggcode-<version>-1.<arch>.rpm` |
| Alpine | `.apk` | `sudo apk add --allow-untrusted ./ggcode-<version>-r1.<arch>.apk` |
| OpenWrt / opkg | `.ipk` | `opkg install ./ggcode_<version>_<arch>.ipk` |
| Arch Linux | `.pkg.tar.zst` | `sudo pacman -U ./ggcode-<version>-1-<arch>.pkg.tar.zst` |

If you prefer not to install from a package manager, the existing release archives, Go installer,
npm wrapper, and Python wrapper remain available.

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
- **`/harness`** runs repo harness workflows like scaffold, checks, and cleanup

## What ggcode can do

From the product point of view, ggcode is more than “chat with a model”:

- **Code understanding** — read files, search the repo, inspect git status and diffs
- **Code changes** — create files, edit targeted regions, and checkpoint edits for undo
- **Command execution** — run foreground commands or long-running background jobs
- **Parallel help** — spawn sub-agents, inspect their progress, and poll long-running workers without blocking the main loop
- **Memory and context** — load project memory files like `GGCODE.md`, `AGENTS.md`, `CLAUDE.md`, and `COPILOT.md`
- **Extensibility** — connect MCP servers, custom plugins, and skills
- **Session continuity** — save, resume, export, and compact conversations
- **Harness workflows** — scaffold repo guidance, enforce invariants, track runs, and garbage-collect stale task state

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
| `/harness` | Open the harness panel for `init/check/monitor/queue/tasks/run/run-queued/review/promote/release/gc/doctor`; `queue` and `run` use the current input draft, and typed `/harness ...` commands still work |
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

For harness-engineering style repos, ggcode also exposes a tracked control plane:

```bash
ggcode harness init --goal "Build an ERP system"
ggcode harness check
ggcode harness queue "Implement purchasing workflow"
ggcode harness queue --context internal-inventory "Implement inventory workflow"
ggcode harness queue --depends-on <purchasing-task-id> "Implement inventory workflow"
ggcode harness tasks
ggcode harness monitor
ggcode harness monitor --watch --interval 2s
ggcode harness contexts
ggcode harness inbox
ggcode harness inbox promote --owner inventory-team
ggcode harness inbox retry --owner unowned
ggcode harness run --all-queued
ggcode harness run --all-queued --retry-failed
ggcode harness run --resume-interrupted --retry-failed
ggcode harness review
ggcode harness review approve <task-id>
ggcode harness review reject <task-id> --note "needs boundary cleanup"
ggcode harness promote
ggcode harness promote apply <task-id>
ggcode harness promote apply --all-approved
ggcode harness release
ggcode harness release --owner inventory-team
ggcode harness release --context internal/inventory
ggcode harness release --environment staging
ggcode harness release --group-by owner
ggcode harness release apply --note "staging wave"
ggcode harness release apply --group-by context --batch-id release-erp
ggcode harness release rollouts --environment prod
ggcode harness release rollouts
ggcode harness release advance rollout-erp
ggcode harness release approve rollout-erp --wave 2 --note "change board approved"
ggcode harness release reject rollout-erp --wave 2 --note "waiting for policy review"
ggcode harness release pause rollout-erp --note "waiting for signoff"
ggcode harness release resume rollout-erp --note "signoff received"
ggcode harness release abort rollout-erp --note "freeze window"
ggcode harness doctor
ggcode harness gc
```

`ggcode harness init` now bootstraps git automatically when the current directory is not yet a repository, and it also creates the harness monitor files up front, including `.ggcode/harness/events.jsonl` and `.ggcode/harness/snapshot.db`. Queued harness runs then default to **isolated worktrees** under `.ggcode/harness/worktrees/<task-id>`, so larger efforts can advance in separate task branches without mutating the repo root working tree.

For larger repos, `harness init` also detects obvious bounded contexts such as `cmd/` and `internal/*`, then creates nested `AGENTS.md` files inside those directories so each subsystem can carry local guidance instead of overloading a single root instruction file.

Queued tasks can also express prerequisites with `--depends-on`, which means larger backlogs can behave like a simple delivery DAG: downstream work stays `blocked` until its prerequisite task reaches `completed`.

Harness backlogs now also support bounded recovery: `run.max_attempts` in `.ggcode/harness.yaml` controls how many times failed tasks may be retried, `ggcode harness run --retry-failed` will re-attempt failed backlog items under that limit, and `ggcode harness run --resume-interrupted` will pick up tasks left in `running` state after an interrupted harness session.

Queued harness execution is now **worker-backed by default** through the same sub-agent lifecycle used elsewhere in ggcode, so running tasks persist worker IDs, phases, and progress summaries while they execute instead of appearing as opaque blocking jobs.

`harness doctor` and `harness gc` now also scan for common entropy signals in larger repos: stale blocked tasks, orphaned worktrees under `.ggcode/harness/worktrees`, and running tasks whose worker state no longer matches the task lifecycle.

Completed harness runs now also persist **delivery evidence**: changed-file lists plus a delivery report with post-run harness verification, so task state reflects what actually changed in the repo instead of only trusting subprocess exit codes.

On top of that, harness now has a lightweight **review loop**: verified completed tasks become review-ready, `harness review` lists them, approvals mark them accepted, and rejections send them back into the retry path as failed tasks with review notes.

Approved tasks can now move into a lightweight **promotion loop** too: `harness promote` shows approved work waiting to land, and `harness promote apply` marks it promoted while also merging the task branch back into the repo when the task ran in a git worktree.

Promoted tasks can then be collected into a lightweight **release loop**: `harness release` builds the current batch plan from promoted-but-unreleased work, `--owner` / `--context` can scope that batch to one routed slice, `--environment` can tag that work for `staging` / `prod` style targets, `--group-by owner|context` can split it into rollout waves, `harness release apply` stamps those tasks with shared release batch IDs plus persisted release reports under the harness logs directory, and `harness release rollouts` / `harness release approve` / `harness release reject` / `harness release advance` / `harness release pause` / `harness release resume` / `harness release abort` let you gate, progress, or deliberately stop grouped waves through a staged rollout sequence.

That staged rollout state is now also visible in the main operational views: `harness doctor`, `harness contexts`, and `harness inbox` all surface rollout activity, including paused and aborted waves, and they now also show rollout gate counts (`pending` / `approved` / `rejected`) instead of forcing you to jump into `harness release rollouts` for every status check.

Harness tasks can now also bind explicitly to a **bounded context**. A context-bound task carries its context metadata into prompts, task state, and verification, and any `commands:` defined under that context in `.ggcode/harness.yaml` run only for that context instead of every task inheriting the whole-repo gate set.

`harness contexts` adds a context-level view over the backlog, so you can see per-context task counts, verification failures, review/promotion/release readiness, and configured command counts without reading the raw task log one item at a time.

Contexts can now also carry lightweight **owner metadata**, so prompts and context reports can tell the next agent who or what team conceptually owns that bounded context.

`harness inbox` turns that owner metadata into an actionable view by grouping **review-ready**, **promotion-ready**, and **retryable** tasks per owner, with an `unowned` bucket for work that still needs routing.

That inbox is no longer read-only: owner-specific batch actions can now promote all promotion-ready tasks for one owner or retry all retryable tasks for one owner without touching unrelated backlogs.

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

For long-running or interactive shell work, the built-in async command tools let the agent start a background command, poll progress, send follow-up stdin input, and stop the job without blocking the whole session.

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
- **`/skills`** is the easiest place to see what is currently available, including MCP prompt-backed skills once those servers are connected

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
