# Providers & Endpoints

Configure which LLM provider ggcode connects to.

## Built-in Vendor Presets

ggcode ships with presets for common providers. Each preset includes a default endpoint URL and model name.

| Vendor | Key | Default Endpoint |
|--------|-----|-----------------|
| OpenAI | `openai` | `https://api.openai.com/v1` |
| Anthropic | `anthropic` | `https://api.anthropic.com` |
| Google (Gemini) | `gemini` | `https://generativelanguage.googleapis.com` |
| OpenRouter | `openrouter` | `https://openrouter.ai/api/v1` |
| DeepSeek | `deepseek` | `https://api.deepseek.com` |
| Moonshot | `moonshot` | `https://api.moonshot.cn/v1` |
| Zhipu | `zhipu` | `https://open.bigmodel.cn/api/paas/v4` |
| Yi | `yi` | `https://api.lingyiwanwu.com/v1` |
| Minimax | `minimax` | `https://api.minimax.chat/v1` |
| SiliconFlow | `siliconflow` | `https://api.siliconflow.cn/v1` |
| Azure OpenAI | `azure` | (deployment-specific) |
| Local (Ollama/LM Studio) | `local` | `http://localhost:11434/v1` |

## Configuration

### Interactive (recommended)

Just run ggcode — it prompts for an API key on first run:

```bash
ggcode
```

### CLI Flags

Override provider settings from the command line:

```bash
ggcode --vendor openai --endpoint https://api.openai.com/v1 --model gpt-4o
```

### Config File

Settings are stored in `~/.ggcode/ggcode.yaml`:

```yaml
vendor: openai
endpoint: https://api.openai.com/v1
model: gpt-4o
```

### API Key Security

The API key is stored in `~/.ggcode/keys.env` — **never** in the YAML file. This keeps secrets out of version control.

```bash
# keys.env (auto-managed)
OPENAI_API_KEY=sk-...
```

## Multiple Endpoints

You can configure multiple vendors and switch between them at runtime. Define each vendor under the `vendors` section:

```yaml
vendors:
  openai:
    endpoints:
      default:
        url: https://api.openai.com/v1
        model: gpt-4o
  deepseek:
    endpoints:
      default:
        url: https://api.deepseek.com
        model: deepseek-chat
```

Switch with flags or the `/mode`-style config commands at runtime.

## Custom Endpoints

Any OpenAI-compatible API works — point `endpoint` at your own server:

```bash
ggcode --vendor openai --endpoint http://localhost:11434/v1 --model llama3
```

## Test Connectivity

Use `llm-probe` to verify your setup and list available models:

```bash
ggcode llm-probe
```
