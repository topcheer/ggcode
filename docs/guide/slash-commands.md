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

## Keyboard Shortcuts

| Key | Action |
|-----|--------|
| `Esc` | Cancel current operation / go back |
| `Ctrl+C` | Cancel current operation |
| `Ctrl+D` | Exit ggcode |
| `Ctrl+R` | Compact conversation |
| `Esc Esc` (double) | Force quit (skip cleanup) |
