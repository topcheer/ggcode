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
- Prompt for your API key
- Write the configuration to `~/.ggcode/ggcode.yaml`
- Store the API key securely in `~/.ggcode/keys.env`

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

## 4. Essential Slash Commands

| Command | Description |
|---------|-------------|
| `/help` | Show available commands |
| `/sessions` | List and resume sessions |
| `/model [name]` | Switch model |
| `/mode [mode]` | Switch permission mode (supervised/plan/auto/bypass/autopilot) |
| `/compact` | Compact conversation to save context |
| `/exit` | Exit ggcode |

## 5. Next Steps

- [Configuration](./configuration.md) — Full config reference
- [Providers](./providers.md) — Set up multiple providers
- [Slash Commands](./slash-commands.md) — All available commands
- [Permission Modes](./modes.md) — Control agent autonomy
- [CLI Reference](./cli-reference.md) — All CLI flags and subcommands
