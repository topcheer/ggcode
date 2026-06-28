# LAN Chat

Real-time messaging between ggcode instances on your local network. Chat with
teammates, their agents, or broadcast to everyone — without leaving your terminal.

## Overview

LAN Chat connects ggcode instances on the same local network via mDNS discovery.
Once connected, you can:

- **Direct message** other participants (human or agent)
- **Broadcast** to all participants at once
- **Team message** — send to all members of a specific team
- **Route** messages to @agent for approval-based agent-to-agent communication
- **Share files** and attachments
- **Track read receipts** and message delivery status

No relay server is required — all communication is P2P over HTTP.

## Nickname, Role, and Team

Each participant has a composite identity with three layers:

| Layer | Default | Example | Purpose |
|-------|---------|---------|---------|
| **Name** | (required) | `alice` | Human-readable display name |
| **Role** | `developer` | `frontend` | Technical specialization |
| **Team** | `dev-team` | `platform` | Team/group for targeted messaging |

The human nick is composed as `<name>_<role>` (e.g. `alice_frontend`).
The agent nick is `<name>_<role>_agent` (e.g. `alice_frontend_agent`).

### Setting Your Identity

```
/nick alice                        # alice_developer @ dev-team
/nick alice@frontend               # alice_frontend @ dev-team
/nick alice@frontend@platform      # alice_frontend @ platform
```

All three layers are optional except the name. Missing parts use defaults.

### Persistence

Role and team are persisted per-session — restarting ggcode restores them.
Starting a new session resets to defaults unless overridden.

## Presence Exchange

When peers discover each other, they exchange presence information including:

- **Node ID** — unique instance identifier
- **Nicks** — human and agent nicks
- **Role** — technical specialization
- **Team** — team/group membership
- **Workspace** — full path to working directory
- **Project name** — basename or git remote name
- **Languages** — detected programming languages (e.g. `go`, `typescript`)
- **Frameworks** — detected frameworks (e.g. `npm`, `flutter`)

This information is visible to all peers and used by the `list` action
to help agents find the right collaborator.

## Messaging

### Direct Messages

Send a message to a specific participant:

```
lanchat(action='send', to='<node_id>', message='Hello!')
```

Find a node ID with `lanchat(action='list')`.

### Messaging Scopes

The `lanchat` tool supports four messaging scopes:

```
lanchat(action='send', to='node-id')          → DM one participant
lanchat(action='send', to='id1,id2,id3')      → DM multiple participants
lanchat(action='broadcast', message='…')       → your team (default scoped)
lanchat(action='send_team', team='platform')   → a specific team
lanchat(action='broadcast_all', message='…')   → everyone on the LAN
```

- **`send`** supports comma-separated recipients for multi-DM
- **`broadcast`** sends to members of **your own team** only
- **`broadcast_all`** sends to every participant regardless of team
- **`send_team`** targets a named team (not your own)

### Team Messaging (`send_team`)

Send a message to all members of a specific team:

```
lanchat(action='send_team', team='platform', message='Deploy is ready')
```

If the team doesn't match any participant, the tool lists valid teams.

### Human vs Agent Recipient

For direct messages, use `to_role` to choose the recipient:

- `to_role='agent'` (default for `send_team`) — deliver to the peer's agent
- `to_role='human'` — show in the peer's chat panel for the human to read

## Agent Integration

Your agent can also send and receive LAN Chat messages via the `lanchat` tool:

```
lanchat(action='list')                                    → discover participants
lanchat(action='send', to='<node_id>', message='…')       → send a DM
lanchat(action='send', to='id1,id2', message='…')         → multi-recipient DM
lanchat(action='broadcast', message='…')                  → your team
lanchat(action='broadcast_all', message='…')              → everyone on LAN
lanchat(action='send_team', team='platform', message='…') → message a team
lanchat(action='history')                                 → read recent messages
lanchat(action='pending')                                 → list pending @agent approvals
lanchat(action='approve', message_id='…')                 → approve an agent message
```

Incoming `@agent` messages require approval before they reach your agent's
conversation. Use `lanchat(action='pending')` to review and approve/reject.

### Team-Based Collaboration

The LLM is aware of team membership through both the tool `list` output and
the system prompt. When a user says "ask the platform team", the LLM will:

1. Call `lanchat(action='list')` to find participants with `team=platform`
2. Use `send_team` to message all platform team members at once, OR
3. Use `send` with a specific `node_id` for a targeted conversation

Three levels of messaging granularity are available to the LLM:

| Action | Reach |
|--------|-------|
| `send` to=`node-id` | One participant (DM) |
| `send_team` team=`name` | All members of a team |
| `send` to=`*` / `broadcast` | Everyone |

### System Prompt

Online instances are listed in the system prompt with their team, role,
workspace, and language info, e.g.:

```
- ggai (/Volumes/new/ggai) — ready [team=platform, role=backend, langs=go]
```

This is injected at the start of every agent turn so the LLM can proactively
identify and collaborate with the right peers.

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
- **Liveness**: HTTP presence exchange (not mDNS). mDNS only discovers peers;
  liveness is determined solely by successful presence probes.
- **Transport**: Direct HTTP between instances (not through a relay server).
- **Authentication**: Built-in community API key (`ggcode-lan-a2a-v1`) for
  zero-config trust between ggcode instances. LAN Chat **always** uses this
  community key for peer-to-peer communication, regardless of any configured
  A2A auth methods (API keys, OAuth2, OIDC, mTLS). This ensures that any two
  ggcode instances on the same LAN can communicate without coordination.
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

### Authentication

LAN Chat always uses the built-in community key (`ggcode-lan-a2a-v1`) for
all peer-to-peer communication. This is hardcoded and cannot be overridden —
it ensures any two ggcode instances on the same LAN can always communicate
regardless of their individual A2A authentication configuration.

If you configure custom A2A auth (e.g., `a2a.auth.api_key`, OAuth2, mTLS),
those settings only affect A2A protocol (agent delegation, tool calls), not
LAN Chat messaging.

## Related

- [A2A Protocol](./a2a.md) — Cross-instance agent delegation and tool calls
- [Multi-Agent Modes](./multi-agent-modes.md) — Sub-agents, teammates, and teams
- [Slash Commands](./slash-commands.md) — Input modes and keyboard shortcuts
