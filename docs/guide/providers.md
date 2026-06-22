# Providers & Endpoints

Configure which LLM provider ggcode connects to.

## Built-in Vendor Presets

ggcode ships with presets for common providers. Each preset includes a default endpoint and model name.

| Vendor | Key | Default Base URL |
|--------|-----|-----------------|
| Z.ai (default) | `zai` | `https://api.z.ai/api/paas/v4` |
| OpenAI | `openai` | `https://api.openai.com/v1` |
| Anthropic | `anthropic` | `https://api.anthropic.com` |
| Google Gemini | `google` | `https://generativelanguage.googleapis.com` |
| DeepSeek | `deepseek` | `https://api.deepseek.com` |
| Moonshot | `moonshot` | `https://api.moonshot.cn/v1` |
| Minimax | `minimax` | `https://api.minimax.chat/v1` |
| Groq | `groq` | `https://api.groq.com/openai/v1` |
| Mistral | `mistral` | `https://api.mistral.ai/v1` |
| XiaoMi MIMO | `xiaomi-mimo` | `https://aiml.xiaomi.com/api/paas/v4` |
| Aliyun Bailian | `aliyun` | `https://dashscope.aliyuncs.com/compatible-mode/v1` |
| Kimi Coding | `kimi` | `https://api.moonshot.cn/v1` |
| Volcengine Ark | `ark` | `https://ark.cn-beijing.volces.com/api/v3` |
| GitHub Copilot | `github-copilot` | (uses GitHub OAuth) |
| AI Gateway | `ai-gateway` | `https://aihubmix.com/v1` |

## Configuration

### Interactive (recommended)

Just run ggcode — the onboarding wizard helps you select a provider and API key on first run:

```bash
ggcode
```

### Config File

Settings are stored in `~/.ggcode/ggcode.yaml`:

```yaml
vendor: openai
endpoint: default          # named endpoint key, NOT a URL
model: gpt-4o
```

The `endpoint` field is a **named endpoint key** (e.g. `default`, `cn-coding-openai`) that maps to an entry under `vendors.<name>.endpoints`. It is not a URL.

### API Key Security

The API key is stored in `~/.ggcode/keys.env` — **never** in the YAML file. This keeps secrets out of version control.

```bash
# keys.env (auto-managed)
OPENAI_API_KEY=sk-...
```

API keys can also be set via environment variables using `${...}` syntax in the YAML:

```yaml
vendor: anthropic
api_key: ${ANTHROPIC_API_KEY}
```

## Multiple Endpoints

You can configure multiple vendors and endpoints, then switch between them at runtime. Define each vendor under the `vendors` section:

```yaml
vendors:
  openai:
    protocol: openai
    endpoints:
      default:
        base_url: https://api.openai.com/v1
        model: gpt-4o
      coding:
        base_url: https://code.openai.com/v1
        model: gpt-4o
  deepseek:
    protocol: openai
    endpoints:
      default:
        base_url: https://api.deepseek.com
        model: deepseek-chat
```

Switch vendors at runtime using the `/provider` slash command or the `config` tool.

## Custom Vendors

Vendors not in the built-in preset list (e.g. OpenRouter, Azure, local LLMs) can be configured manually:

```yaml
vendors:
  openrouter:
    protocol: openai
    endpoints:
      default:
        base_url: https://openrouter.ai/api/v1
        model: anthropic/claude-sonnet-4
  local:
    protocol: openai
    endpoints:
      default:
        base_url: http://localhost:11434/v1
        model: llama3
```

## Supported Protocols

| Protocol | Description |
|----------|-------------|
| `openai` | OpenAI-compatible API (most providers) |
| `anthropic` | Anthropic Claude native API |
| `gemini` | Google Gemini native API |
| `copilot` | GitHub Copilot (OAuth-based, no API key needed) |

## Test Connectivity

Use `llm-probe` to verify your setup and list available models:

```bash
ggcode llm-probe
```
