# LAN Chat

Real-time messaging between ggcode instances on your local network. Chat with
teammates, their agents, or broadcast to everyone — without leaving your terminal.

## Overview

LAN Chat connects ggcode instances on the same local network via mDNS discovery.
Once connected, you can:

- **Send direct messages** to any online user or their agent
- **Broadcast** to all connected instances
- **Quick-reply** from the main input with `#` mode
- **Manage @agent approvals** — approve or reject incoming agent-to-agent messages
- **Use `/chat` panel** for a full chat UI with mention autocomplete

No configuration required — discovery and authentication are automatic.

## Quick Start

### Send a message

Type `#` in an empty input to enter chat mode:

```
#  <prompt becomes #>
@teammate hello, can you help me debug this?
```

- **`#`** enters chat mode (prompt changes to `# `)
- **`@`** opens the user list (online participants + "All" for broadcast)
- **Enter** sends the message; you stay in chat mode
- **Esc** exits chat mode

### Reply to unread messages

When a LAN Chat message arrives and the chat panel is closed, the system message
shows the sender and content:

```
[LAN Chat] alice: hey, check the latest commit — # to reply
```

Type `#` — the input auto-fills `@alice ` so you can type your reply immediately.

### Open the full chat panel

```
/chat
```

Opens a dedicated panel with:
- Message history
- @mention autocomplete for users
- `/nick <name>[@role]` to set your nickname and role (e.g. `/nick alice@frontend` → nick `alice_frontend`, role `frontend`). Without `@role`, defaults to `developer`.
- `@agent` mention to route messages to the user's agent instead of the human

## Usage

### `#` Chat Mode (Quick Send)

| Action | Key |
|--------|-----|
| Enter chat mode | `#` in empty input |
| Pick a user | `@` → select from list |
| Broadcast | Select "All" from the list, or just type without `@` |
| Send message | Type text + `Enter` |
| Exit chat mode | `Esc` |

**Unread auto-fill**: If there are unread messages when you press `#`, the input
pre-fills `@lastSender` so you can reply instantly.

**`@` in chat mode vs normal mode**: Inside chat mode, `@` shows the LAN Chat user
list. Outside chat mode, `@` triggers file mention autocomplete. These do not conflict.

### `/chat` Panel

| Command | Description |
|---------|-------------|
| `/chat` | Open the LAN Chat panel |
| `/nick <name>[@role]` | Set nickname and role (e.g. `alice@frontend`). Default role: `developer` |
| `@nick message` | Send a DM to a user |
| `@nick_agent message` | Send a DM to the user's agent |
| Plain text | Broadcast to all connected instances |

### Agent Integration

Your agent can also send and receive LAN Chat messages via the `lanchat` tool:

```
lanchat(action='list')                              → discover participants
lanchat(action='send', to='<node_id>', message='…') → send a message
lanchat(action='history')                           → read recent messages
lanchat(action='pending')                           → list pending @agent approvals
lanchat(action='approve', message_id='…')           → approve an agent message
```

Incoming `@agent` messages require approval before they reach your agent's
conversation. Use `lanchat(action='pending')` to review and approve/reject.

## How It Works

```
ggcode instance A (mDNS broadcast)  ←→  ggcode instance B (mDNS broadcast)
         ↓                                        ↓
    HTTP P2P mesh                     HTTP P2P mesh
         ↓                                        ↓
    LAN Chat Hub                      LAN Chat Hub
    (messages, approvals)             (messages, approvals)
```

- **Discovery**: mDNS (`_ggcode._tcp`) on the local network. Automatic, no config.
- **Transport**: Direct HTTP between instances (not through a relay server).
- **Authentication**: Built-in community API key (`ggcode-lan-a2a-v1`) for
  zero-config trust between ggcode instances.
- **Privacy**: Messages stay on your LAN — nothing goes through external servers.

## Desktop GUI

LAN Chat is also available in ggcode Desktop (Wails app). Click the chat bubble
icon in the sidebar to open the LAN Chat tab. Features:

- **Multi-room view**: Broadcast room + separate DM rooms per participant
- **Unread badges**: Per-room unread count with total badge in sidebar
- **Contact list**: Shows online participants with human/agent nick separation
- **@agent approvals**: Approve or reject incoming agent messages inline
- **Attachments**: Drag-and-drop file sharing between instances

## Attachments

Share files with teammates directly in chat. In the `/chat` panel or desktop GUI:

1. Drag a file into the chat area, or use the attachment button
2. The file is Base64-encoded and sent over the P2P mesh
3. The recipient sees a download link inline

Attachments are limited to 10 MB to keep the P2P mesh responsive.

## Configuration

LAN Chat is enabled by default. To disable:

```yaml
a2a:
  lan_discovery: false
```

To use a custom API key instead of the community key:

```yaml
a2a:
  auth:
    api_key: "your-team-secret"
```

All team members must share the same key.

## Related

- [A2A Protocol](./a2a.md) — Cross-instance agent delegation and tool calls
- [Multi-Agent Modes](./multi-agent-modes.md) — Sub-agents, teammates, and teams
- [Slash Commands](./slash-commands.md) — Input modes and keyboard shortcuts
