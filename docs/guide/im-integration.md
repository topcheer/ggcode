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

## Runtime Behavior

- IM adapters **auto-start** when the daemon launches.
- Users send prompts from their IM client; ggcode responds directly in the chat.
- IM adapters can be enabled/disabled/muted at runtime without restarting.
- Mute state is in-memory — daemon restart recovers all adapters unmuted.
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
