# IM Platform Integration

Connect ggcode to instant messaging platforms so you can send prompts and receive responses directly from your chat client.

## Supported Platforms

| Platform | Key |
|----------|-----|
| QQ | `qq` |
| Telegram | `telegram` |
| Discord | `discord` |
| Slack | `slack` |
| Feishu (Lark) | `feishu` |
| DingTalk | `dingtalk` |
| PrivateClaw | `privateclaw` |

## Setup

### Interactive Wizard

```bash
ggcode im config add
```

Launches an interactive wizard that walks you through platform selection and credential entry.

### CLI Setup

Add an adapter non-interactively:

```bash
ggcode im config add my-qq \
  --platform qq \
  --extra app_id=xxx \
  --extra app_secret=xxx \
  --extra token=xxx
```

### View Status

```bash
ggcode im status
```

### List Adapters

```bash
ggcode im config list
```

### Remove an Adapter

```bash
ggcode im config remove my-qq
```

## Runtime Behavior

- Once configured, IM adapters **auto-start** when the ggcode TUI launches.
- Users send prompts from their IM client; ggcode responds directly in the chat.
- Share a session with an IM channel:

```bash
ggcode im share
```

## Required Credentials per Platform

| Platform | Required Keys |
|----------|--------------|
| QQ | `app_id`, `app_secret`, `token` |
| Telegram | `token` |
| Discord | `token` |
| Slack | `token`, `signing_secret` |
| Feishu (Lark) | `app_id`, `app_secret` |
| DingTalk | `app_key`, `app_secret` |
| PrivateClaw | `token`, `endpoint` |
