# Slash Commands

Slash commands are typed directly in the chat input within the TUI.

## Session & Conversation

| Command | Description |
|---------|-------------|
| `/help` | Show available commands |
| `/sessions` | List and resume sessions |
| `/resume` | Resume a specific session |
| `/compact` | Compact conversation to save context |
| `/clear` | Clear conversation |
| `/stats` | Open session statistics panel |
| `/exit` | Exit ggcode |
| `/restart` | Restart ggcode (preserves current session) |
| `/update` | Check and install updates |

## Model & Provider

| Command | Description |
|---------|-------------|
| `/model [name]` | Show or switch the current model |
| `/provider [vendor] [endpoint]` | Show or switch LLM provider |
| `/impersonate [provider/model]` | Impersonate a provider/model for testing |
| `/lang [en\|zh-CN]` | Switch interface language |
| `/stream` | Toggle stream mode |

## Permission & Configuration

| Command | Description |
|---------|-------------|
| `/mode [supervised\|plan\|auto\|bypass\|autopilot]` | Switch permission mode |
| `/config` | Configuration management |
| `/init` | Create `GGCODE.md` project memory file |
| `/memory` | Manage project memory |
| `/undo` | Undo last file changes |
| `/checkpoints` | Show available checkpoints |

## Tools & Integrations

| Command | Description |
|---------|-------------|
| `/mcp` | Show MCP server status |
| `/im` | Show IM adapter status |
| `/skills` | Manage skills |
| `/plugins` | Manage plugins |
| `/image` | Image handling |
| `/todo` | View/manage todo list |
| `/status` | Show current status |

## Sharing & Tunnel

| Command | Description |
|---------|-------------|
| `/share` / `/tunnel` | Start sharing session via tunnel |
| `/unshare` | Stop sharing |

## Advanced

| Command | Description |
|---------|-------------|
| `/harness [subcommand]` | Run harness commands |
| `/knight` | Knight auto-evolution |
| `/tmux` | Manage tmux session |
| `/bug` | Report a bug |

## IM-Specific Commands

Per-platform commands are available when IM adapters are configured:

| Command | Description |
|---------|-------------|
| `/qq` | QQ adapter |
| `/telegram` / `/tg` | Telegram adapter |
| `/discord` | Discord adapter |
| `/feishu` / `/lark` | Feishu/Lark adapter |
| `/slack` | Slack adapter |
| `/dingtalk` / `/ding` | DingTalk adapter |
| `/wechat` | WeChat adapter |
| `/wecom` | WeCom adapter |
| `/mattermost` / `/mm` | Mattermost adapter |
| `/matrix` | Matrix adapter |
| `/signal` | Signal adapter |
| `/irc` | IRC adapter |
| `/nostr` | Nostr adapter |
| `/whatsapp` / `/wa` | WhatsApp adapter |
| `/twitch` | Twitch adapter |

## Input Modes

The TUI input supports three modes, indicated by the prompt prefix:

| Mode | Trigger | Prompt | Description |
|------|---------|--------|-------------|
| Normal (default) | — | `❯` | Standard chat input. Messages go to the agent. |
| Shell mode | `$` or `!` | `$` | Run shell commands inline. Output is captured and shown in the chat. Exits after each command. Press `Esc` to exit. |
| Chat mode | `#` | `#` | Send LAN Chat messages without opening the `/chat` panel. Stays active until you press `Esc`. |

**Shell and chat modes work independently of the agent.** You can enter `$` or `#`
and send commands/messages even while the agent is running. Shell commands execute
immediately and do not enter the agent queue. Chat messages are sent via LAN Chat
and never reach the agent. Only normal-mode text is queued for the agent when busy.

### Chat Mode (`#`)

Quick-send messages to other ggcode instances on your LAN directly from the main input:

1. Type `#` in an empty input to enter chat mode.
2. A user list appears showing online participants (plus "All" for broadcast).
3. Select a target (or "All" for broadcast), type your message, and press `Enter`.
4. **Stays in chat mode** — type another message without re-entering the mode.
5. Press `Esc` to exit.

**Unread messages**: When there are unread LAN Chat messages, entering `#` auto-fills `@sender` so you can reply immediately.

**`@` inside chat mode**: Press `@` to bring up the user list again. This does not conflict with `@file` mentions in normal mode.

## Keyboard Shortcuts

| Key | Action |
|-----|--------|
| `Esc` | Cancel current operation / exit shell/chat mode / go back |
| `Ctrl+C` | Cancel current operation |
| `Ctrl+D` | Exit ggcode |
| `Ctrl+R` | Compact conversation |
| `Esc Esc` (double) | Force quit (skip cleanup) |
