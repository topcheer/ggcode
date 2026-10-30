# Plain Mode (Linear Frontend)

`--plain` starts ggcode in a line-oriented frontend with no Bubble Tea TUI: line-buffered stdin, linear streaming output, no raw mode, no frame refresh. It exists for environments the full-screen TUI cannot serve — dumb terminals, screen readers, restricted SSH sessions, or anyone who prefers plain line IO.

The same frontend is **auto-selected** when `TERM=dumb` or `TERM` is unset, so those environments never see garbled frames.

```bash
ggcode --plain          # explicit
TERM=dumb ggcode        # automatic
```

It rides the same interactive runtime core as the TUI — agent, tools, sessions, and config hot-swap all work; only the visual panels are absent. A2A server, LAN chat, and other TUI-shaped services are not started on this path.

## Output Format

LLM text streams to the terminal as it arrives. Each tool call prints one boundary line with its result status:

```
[tool] glob (ok)
[tool] run_command (error)
```

Tool arguments and raw results are not echoed (they would drown the prose); `(ok)`/`(error)` is the per-call verdict. A call whose result never arrives still gets its line closed when the turn ends.

## Slash Commands

Anything not starting with `/` is sent to the agent as a conversation turn.

| Command | Description |
|---------|-------------|
| `/status` | Show active vendor/endpoint/model |
| `/model <name>` | Hot-switch model (persists) |
| `/provider <vendor> [endpoint]` | Switch vendor (optionally endpoint) |
| `/provider --list [--all]` | List endpoints grouped by vendor, up to 10 models each (`+N more` hint). Default shows only endpoints with an API key **and** a configured model list; `--all` includes the rest, annotated `(no api key)` / `(no models configured)`. The active endpoint is marked `*` |
| `/provider --add-endpoint ...` | Add an endpoint to the **active vendor** (see below) |
| `/endpoint <id>` | Switch endpoint on the current vendor |
| `/endpoint --show-models` | All models on the active endpoint (static config merged with live discovery) |
| `/endpoint --refresh-models` | Force a model refetch, bypassing the 6h discovery cache, then show |
| `/endpoint --add-model ...` | Add a model to the **active endpoint** (see below) |
| `/endpoint --set-apikey --apikey <key>` | Set the API key on the active endpoint |
| `/lang <en\|zh-CN>` | Switch interface language (persists globally) |
| `/help`, `/exit` | Command list; leave the REPL (Ctrl-D also works) |

### /provider --add-endpoint

```bash
/provider --add-endpoint \
  --endpoint-name my-relay \
  --endpoint-type openai \
  --endpoint-url https://relay.example.com/v1 \
  --endpoint-model gpt-x \
  --context-window 200000 \
  --max-output 32768 \
  --endpoint-apikey sk-...
```

`--endpoint-type` must be one of `openai`, `openai-responses`, `anthropic`, `gemini`, `copilot`. Duplicate endpoint ids are refused. The endpoint lands on the currently active vendor.

### /endpoint --add-model

```bash
/endpoint --add-model --model glm-5.3 [--apikey sk-...]
```

Appends a model to the **active endpoint** (deduplicated; optional `--apikey` updates the endpoint key at the same time). Unlike `/provider --add-endpoint`, this never creates an endpoint — the active one is the target by definition. Switch to the new model afterwards with `/model <name>`.

## Behavior Differences

- **No autopilot injections.** Even when the global default mode is `bypass`/`autopilot`, the linear frontend skips the goal-collection meta-instruction and the end-of-turn strategist prompt — a text answer ends the run and returns to the prompt. The permission surface itself is unchanged (still bypass if configured); only the behavioral injections are suppressed.
- **Approvals default to Accept.** There is no dialog UI; set `GGCODE_PLAIN_APPROVE=deny` to reject instead.
- **ask_user is disabled.** The agent ends its turn and waits; your next typed line is the answer — multi-turn conversation is the native pause point.
- **HOME-directory warning is line-oriented**: the same localized warning text as the TUI, confirmed with a `y/N` prompt instead of a full-screen dialog.
- **Language is global.** `/lang` persists to the `language` config field shared with the TUI (see [Configuration](configuration.md)); the session picks it up at startup, `en` when unset.

## Related

- [CLI Reference](cli-reference.md) — global flags
- [Configuration](configuration.md) — `vendors`/`endpoints`/`language` schema
