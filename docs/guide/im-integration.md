# IM Platform Integration

Connect ggcode to instant messaging platforms so you can send prompts and receive responses directly from your chat client.

## Supported Platforms

| Platform | Key | Required Credentials |
|----------|-----|---------------------|
| QQ | `qq` | `app_id`, `app_secret`, `token` |
| Telegram | `telegram` | `token` |
| Discord | `discord` | `token` |
| Slack | `slack` | `token`, `signing_secret` |
| Feishu (Lark) | `feishu` | `app_id`, `app_secret` |
| DingTalk | `dingtalk` | `app_key`, `app_secret` |
| WeChat | `wechat` | `token` |
| WeCom | `wecom` | `token` |
| Mattermost | `mattermost` | `url`, `token` |
| Matrix | `matrix` | `homeserver`, `user_id`, `access_token` |
| Signal | `signal` | `phone_number` |
| IRC | `irc` | `server`, `nick`, `channel` |
| Nostr | `nostr` | `private_key` |
| Synology Chat | `synology` | `url`, `token` |
| Twitch | `twitch` | `token`, `client_id` |
| WhatsApp | `whatsapp` | `phone_number` |

## Configuration

### Add an Adapter

```bash
ggcode im config add --platform telegram --name my-tg --token YOUR_TOKEN
```

### List Adapters

```bash
ggcode im config list
```

### Remove an Adapter

```bash
ggcode im config remove my-tg
```

## Daemon Mode Slash Commands

In daemon mode, the following slash commands are available in any connected IM channel:

| Command | Description |
|---------|-------------|
| `/listim` | List all IM adapters with status (online/muted/active) |
| `/muteim <name>` | Mute a specific adapter (cannot mute yourself) |
| `/muteall` | Mute all adapters except the one you're messaging from |
| `/muteself` | Mute the current adapter — stops all replies |
| `/restart` | Restart daemon (unmutes all — mute is in-memory) |
| `/help` | Show available commands |

## LLM Tool: `im`

The agent can manage IM adapters and send messages via the `im` tool. This tool is available in all permission modes (always allowed, no confirmation needed).

### Actions

| Action | Parameters | Description |
|--------|-----------|-------------|
| `status` | — | List all adapters for the current workspace with health, platform, muted/disabled state, and channel info. Shows a warning if other instances have active channels. |
| `mute` | `adapter` | Mute an adapter: drops the connection, suppresses inbound/outbound. Binding stays active for fast restore. |
| `unmute` | `adapter` | Unmute: reconnects and resumes message flow. |
| `disable` | `adapter` | Disable an adapter: moves binding to disabled state, drops connection. |
| `enable` | `adapter` | Re-enable: moves binding back, reconnects. |
| `send` | `adapter`, `message`, `auto_start?` | Send a text message to the adapter's bound channel. |

### Send with `auto_start`

By default, `send` only works on active (unmuted + enabled + healthy) adapters. When `auto_start=true`:

1. Checks if other instances in the same workspace have active IM channels (via `InstanceDetect`). If so, refuses to start a competing connection — reports the conflict instead.
2. If no conflict: calls `unmute` or `enable` to activate the adapter.
3. Waits up to 15 seconds for the adapter to become healthy.
4. Sends the message via `SendDirect` (targets the specific adapter, not all bound channels).

```
{"action": "send", "adapter": "tg-bot", "message": "Deploy complete", "auto_start": true}
```

### Mute vs Disable

Both operations **drop the connection** (cancel adapter context + close sink). The difference:

| | `mute` | `disable` |
|---|--------|----------|
| Connection | Dropped | Dropped |
| Binding location | `currentBindings` (stays active) | `disabledBindings` (moved out) |
| UI shows | Muted badge | Removed from active list |
| Restore speed | Faster (unmute) | Slower (enable) |
| Persisted | No (in-memory) | No (in-memory) |

### Workspace Scope

All status/bindings are workspace-scoped: the tool only shows and operates on adapters bound to the current workspace's session.

### Binding Hot Watcher

The IM runtime includes a **binding hot watcher** (`binding_watcher.go`) that monitors `~/.ggcode/im-bindings.json` for changes by other ggcode instances. The watcher polls every 3 seconds and detects when another instance claims a binding (different `LastSessionID`). When this happens, the watcher auto-mutes the affected adapter to prevent conflicts. This ensures only one instance per session can actively use an IM channel. The watcher stops on `UnbindSession` and restarts on `BindSession` with the new session ID.

## Runtime Behavior

- IM adapters **auto-start** when the daemon launches.
- Users send prompts from their IM client; ggcode responds directly in the chat.
- IM adapters can be enabled/disabled/muted at runtime without restarting — via slash commands, the TUI IM panel, or the `im` tool.
- Mute state is in-memory — daemon restart recovers all adapters unmuted. Disable state is also in-memory.
- Tool result delivery granularity is controlled by `im.output_mode`:
  - `verbose` (default) — full tool results
  - `quiet` — minimal output
  - `summary` — summarized results

## Sharing Sessions

Share a session with an IM channel:

```bash
ggcode im share
```

This mirrors the agent's output to the IM channel, allowing remote monitoring and interaction.
