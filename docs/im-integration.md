# IM Integration Guide

Connect ggcode to messaging platforms (QQ, Telegram, Discord, Feishu, Slack, DingTalk, WeChat/WeCom, Mattermost, Matrix, Signal, IRC, Nostr, Twitch, WhatsApp, Synology).

## Start here: the /im cockpit (#4861)

Open `/im` — the panel's default view IS the onboarding cockpit:

- **Top block**: the full-state platform registry (every platform, including never-configured ones) plus any in-progress onboarding row.
- **Every platform onboards in its own existing panel** (`/qq`, `/telegram`, `/feishu`, ...): select a channel row and press **`o`** to open that platform's panel directly. The panel carries the numbered setup steps with anti-confusion notes (the BotFather token is not your @username; the Feishu App Secret is not the Verification Token; the Slack webhook is post-only; WeCom CorpID/AgentId/Secret are three different things), auto-opening entry deep-links, and — on QR-pairing platforms like QQ/WhatsApp — large terminal QR rendering with a URL fallback line.
- **Verification**: after saving credentials (masked input, in-panel), the registry row must read `connected`. Failed/expired rows carry the adapter's last error, so a bad credential is visible immediately — fix and repeat; no live traffic needed.
- **First-use bridge**: close the loop by sending the onboard self-test through the channel itself (`im` tool, action `send`) or by issuing any command from the bound channel — the reply is the proof.
- **Resume**: onboarding progress persists in `.ggcode/im-onboarding.json` (no secrets stored). An interrupted run shows as an in-progress row in the cockpit; resume inside the platform's panel. Press **`x`** twice to clear the record (two-step confirm — a stray keypress cannot destroy cross-session progress). Editing the config file invalidates stale wizard progress.

## Full-state registry

`/im status` shows every platform in one table:

| State | Meaning |
|---|---|
| connected | adapter healthy in the runtime snapshot |
| failed | adapter running but erroring (last error shown) |
| expired | failing with an auth/token-class error (credential needs renewal) |
| muted / disabled | bound but silenced / disabled |
| not connected | configured but the adapter is not running |
| never-configured | platform available in the registry, nothing set up |

## Manual configuration

The wizard is the recommended path; manual entry remains fully supported via the per-platform panels and `ggcode.yaml` (`im.adapters`), as before.
