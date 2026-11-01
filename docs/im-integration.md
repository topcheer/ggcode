# IM Integration Guide

Connect ggcode to messaging platforms (QQ, Telegram, Discord, Feishu, Slack, DingTalk, WeChat/WeCom, Mattermost, Matrix, Signal, IRC, Nostr, Twitch, WhatsApp, Synology).

## Start here: the onboarding wizard (#4861)

```
/im onboard            # platform picker + resume notice for an interrupted run
/im onboard telegram   # numbered steps with anti-confusion notes for one platform
/im status             # full-state registry: every platform, including unconfigured ones
/im onboard done       # clear the onboarding record
```

The wizard walks one platform end-to-end:

1. **Numbered setup steps** with anti-confusion notes for the classic traps (the BotFather token is not your @username; the Feishu App Secret is not the Verification Token; the Slack webhook is post-only; WeCom CorpID/AgentId/Secret are three different things).
2. **Credential entry** happens in the existing per-platform panel the wizard points you to (`/qq`, `/telegram`, ...). Secrets are masked on input there. The pairing flows (QQ QR-pairing, BotToken lanes) are unchanged - the wizard only guides you to them.
3. **Setup-time verification**: after saving, run `/im status` - the platform's row must read `connected`. Failed/expired rows carry the adapter's last error, so a bad credential is visible immediately, not on the first bounced message. Fix and repeat; no live traffic needed.
4. **First-use bridge**: close the loop by sending the onboard self-test through the channel itself (`im` tool, action `send`) or by issuing any command from the bound channel - the reply is the proof.
5. **Resume**: progress persists in `.ggcode/im-onboarding.json` (no secrets stored). An interrupted run is offered for resume on the next `/im onboard`. Editing the config file invalidates stale wizard progress.

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
