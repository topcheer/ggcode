# Getting Started

## 1. Install ggcode

See the [Installation Guide](./install.md) for all available methods.

## 2. Configure Your API Key

On first run, ggcode auto-detects your environment and prompts for configuration:

```bash
ggcode
```

Alternatively, set your vendor, endpoint, and model manually.

**OpenAI:**

```bash
ggcode config set vendor openai
ggcode config set endpoint openai
ggcode config set model gpt-4o
ggcode config set api_key sk-...
```

**Anthropic:**

```bash
ggcode config set vendor anthropic
ggcode config set endpoint anthropic
ggcode config set model claude-sonnet-4-20250514
ggcode config set api_key sk-ant-...
```

**Local endpoint (e.g. Ollama):**

```bash
ggcode config set vendor openai
ggcode config set endpoint http://localhost:11434
ggcode config set model llama3
```

Verify your configuration:

```bash
ggcode llm-probe
```

## 3. Start Coding

Navigate to your project and launch ggcode:

```bash
cd your-project
ggcode
```

## 4. Basic Interactions

| Action | Command |
|--------|---------|
| Ask a question | Type your prompt and press Enter |
| View help | `/help` |
| Update ggcode | `/update` |
| List sessions | `/sessions` |

## 5. TUI Shortcuts

| Shortcut | Action |
|----------|--------|
| `Ctrl+C` | Cancel current operation |
| `Ctrl+L` | Clear the screen |
| `Tab` | Switch between panes |
| `/` | Open command menu |

---

Next: [CLI Reference](./cli-reference.md)
