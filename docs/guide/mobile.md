# ggcode Mobile

iOS and Android app to monitor and interact with ggcode sessions on the go.

## Overview

ggcode Mobile connects to your desktop or CLI ggcode instance via a relay server, giving you remote access to your agent from anywhere.

## Installation

| Platform | Source |
|----------|--------|
| iOS | TestFlight |
| Android | Google Play |

## Pairing

Pair your mobile device by scanning a QR code generated from either:

- The desktop app (Settings → Pair Mobile)
- The CLI:

```bash
ggcode mobile pair
```

Scan the QR code with the ggcode Mobile app to complete pairing.

## Features

- **Notifications** — receive push alerts for task completions, errors, and review requests
- **Code review** — review diffs and code changes directly on your phone
- **Tool approvals** — approve or deny tool call requests remotely
- **Chat** — send prompts and receive responses from your ggcode agent

## Security

- **End-to-end encrypted** relay — your data is encrypted between your device and your ggcode instance.
- **No data stored** on the relay server. The relay only routes encrypted packets.
- Pairing keys are device-specific and revocable at any time.
