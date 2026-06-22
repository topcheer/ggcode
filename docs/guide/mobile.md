# ggcode Mobile

iOS and Android app to monitor and interact with ggcode sessions on the go.

## Overview

ggcode Mobile connects to your desktop or CLI ggcode instance via a relay server, giving you remote access to your agent from anywhere.

## Installation

| Platform | Source |
|----------|--------|
| iOS | [App Store](https://apps.apple.com/us/app/ggcode-mobile/id6770855612) · [TestFlight](https://testflight.apple.com/join/J34wVD6p) |
| Android | [Google Play](https://play.google.com/store/apps/details?id=gg.ai.ggcode.mobile) |

## Pairing

Pair your mobile device by scanning a QR code generated from either:

- The desktop app (Settings → Pair Mobile)
- The CLI:

```bash
ggcode mobile pair
```

Scan the QR code with the ggcode Mobile app to complete pairing.

## Features

### Chat & Interaction
- **Chat** — send prompts and receive responses from your ggcode agent
- **Streaming** — real-time streaming of text and reasoning
- **Tool visualization** — view tool calls and their results
- **Session resume** — sessions survive disconnects and app restarts

### Remote Control
- **Tool approvals** — approve or deny tool call requests remotely
- **Code review** — review diffs and code changes directly on your phone
- **ask_user responses** — answer structured questions from the agent
- **Interrupt** — cancel a running agent from your phone

### Session Management
- **Multi-session** — switch between multiple sessions
- **Mode switching** — change permission mode (supervised/plan/auto/bypass/autopilot)
- **Language sync** — interface language follows desktop setting
- **Notifications** — receive push alerts for task completions, errors, and review requests

## How Tunneling Works

```
ggcode instance (desktop/CLI)
    ↓ tunnel broker (WebSocket, event persistence)
Relay server
    ↓ WebSocket (encrypted)
ggcode Mobile app
```

1. The desktop/CLI instance runs a **tunnel broker** that records agent events
2. Events are sent to the relay server via WebSocket with backpressure
3. The mobile app connects to the relay and receives events in real-time
4. On reconnect, historical events are replayed for continuity

## Security

- **End-to-end encrypted** — your data is encrypted between your device and your ggcode instance
- **Relay persistence** — the relay stores encrypted event data in SQLite for session replay; events are deduplicated by event ID to prevent history bloat
- **Pairing keys** — device-specific and revocable at any time
- **Session-scoped** — each room is keyed by workspace, preventing cross-project data leakage
