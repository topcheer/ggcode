# Getting Started

## 1. Install ggcode

See the [Installation Guide](./install.md) for all available methods.

## 2. Configure Your API Key

On first run, ggcode auto-detects your environment and prompts for configuration:

```bash
ggcode
```

The onboarding wizard will:
- Ask you to select a provider (Z.ai, OpenAI, Anthropic, etc.)
- Prompt for your API key - **required** for any provider that needs one (the step will not
  advance on an empty field unless the provider's API key environment variable is already set)
- Write the configuration to `~/.ggcode/ggcode.yaml`
- Store the API key securely in `~/.ggcode/keys.env` (the vendor file keeps only a `${VAR}` reference)

If the wizard ever finishes without a usable key, it now says so instead of starting into a
provider that answers 401 `API 密钥无效或未配置`:

```
Warning: onboarding finished without a usable API key for deepseek/api.
  Fix it by re-running the wizard (`ggcode onboard`), or without it:
    - export DEEPSEEK_API_KEY in your shell/profile, or
    - add it to ~/.ggcode/keys.env as: export DEEPSEEK_API_KEY='<your key>'
```

A headless start (`--plain`, `TERM=dumb`, or no TTY) prints the same remediation **before** the
first message instead of letting it fail with 401:

```
[notice] no usable API key for deepseek/api - the first message will fail with 401.
  Fix it with `ggcode onboard`, or without the wizard:
    - export DEEPSEEK_API_KEY='<your key>', or
    - add it to ~/.ggcode/keys.env as: export DEEPSEEK_API_KEY='<your key>'
```

Re-run the wizard at any time with `ggcode onboard` (to fix a key or switch provider). Onboarding
does not re-open on its own once a config file exists; `GGCODE_SKIP_AUTOCONFIG` disables every
automatic configuration decision, and a loopback base URL or `auth_type: none` endpoint needs no key.

You can also manually create the config file. See [Configuration](./configuration.md) for details.

### Manual Setup

Create `~/.ggcode/ggcode.yaml`:

```yaml
vendor: openai
endpoint: default
model: gpt-4o
```

Then add your API key to `~/.ggcode/keys.env`:

```bash
OPENAI_API_KEY=sk-your-key-here
```

Or set it via environment variable:

```bash
export OPENAI_API_KEY=sk-your-key-here
```

## 3. Start Chatting

```bash
ggcode
```

Type a message and press Enter. ggcode will:
1. Send your message to the configured LLM
2. Stream the response in real-time
3. Execute any tool calls (file reads, searches, etc.) with your approval
4. Close the turn with a system line showing when the agent finished - `Finished at 2026-11-24 09:08:07`
   (`Canceled at` / `Failed at` for the other outcomes) - so if you step away for hours you can see at a glance
   when the work stopped. Resumed sessions print a recap with the last-active time for the same reason.

The terminal / tab title (OSC 0) follows the session so a strip full of
instances stays readable: while the agent is working it reads
`> <activity> — <workspace> [model]` (e.g. `> Thinking ... — burrow [glm-5.4]`),
and when it is idle just `> <workspace> [model]` (e.g. `> ggcode [glm-5.4]`) -
no product-name prefix, so the workspace name keeps the width.

### Input Modes

The input supports three modes:

- **Normal** (`❯`): Chat with the agent (default).
- **Shell** (`$`): Type `$` or `!` to run shell commands inline. Exits after each command.
- **Chat** (`#`): Type `#` to send LAN Chat messages to other ggcode instances. Stays active until `Esc`.

## 4. Essential Slash Commands

| Command | Description |
|---------|-------------|
| `/help` | Show available commands |
| `/sessions` | List and resume sessions |
| `/model [name]` | Switch model |
| `/mode [mode]` | Switch permission mode (supervised/bypass; auto/autopilot accepted as legacy aliases) |
| `/compact` | Compact conversation to save context |
| `/exit` | Exit ggcode |

The agent itself can also reclaim context by calling the `compact_context`
tool (Context-as-Tool): it runs a cheap mechanical pass immediately and, at
or above the auto-compact threshold, schedules background summarization that
applies at the next turn boundary.

Compaction is lossy, so anything it removes (summarized-away turns and
superseded tool results) is kept in a session-scoped archive (~1MB, oldest
entries evicted first). The agent can retrieve the verbatim original text
later via the read-only `recall_compacted` tool — e.g. to recover an exact
error message or command output that predates the last summary — instead of
re-running the work.

## 5. Next Steps

- [Configuration](./configuration.md) — Full config reference
- [Providers](./providers.md) — Set up multiple providers
- [Slash Commands](./slash-commands.md) — All available commands and input modes
- [Permission Modes](./modes.md) — Control agent autonomy
- [LAN Chat](./lan-chat.md) — Real-time messaging between instances
- [CLI Reference](./cli-reference.md) — All CLI flags and subcommands
