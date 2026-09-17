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
| `openai-responses` | OpenAI Responses API (`/v1/responses`), required for Codex-family and o-series models |
| `anthropic` | Anthropic Claude native API |
| `gemini` | Google Gemini native API |
| `copilot` | GitHub Copilot (OAuth-based, no API key needed) |

An endpoint with protocol `openai` whose `base_url` ends in `/responses` is
also routed to the Responses API automatically.

### OpenAI Responses API

Codex models (e.g. `gpt-5-codex`) and o-series reasoning models are served
through OpenAI's Responses API, which uses a different wire protocol from Chat
Completions. Use `protocol: openai-responses` to reach them:

```yaml
vendors:
  openai:
    protocol: openai-responses
    reasoning_effort: high
    endpoints:
      default:
        base_url: https://api.openai.com/v1
        model: gpt-5-codex
```

`reasoning_effort` and `tool_choice` (`auto` / `required` / `none`) are both
supported on this protocol. Tool calls use the Responses API's native
`function_call` / `function_call_output` items, so no prompt-injection fallback
is involved.

ggcode runs the protocol statelessly (`store: false`): every request replays
the full conversation. Reasoning models keep their chain-of-thought across
tool calls because ggcode requests `include: ["reasoning.encrypted_content"]`
and echoes the encrypted reasoning items it received back verbatim on the next
request, per OpenAI's stateless multi-turn guidance.

## Text Verbosity

GPT-5 models accept a `text.verbosity` output-length hint that independently
controls how verbose responses are (this is about answer length, not reasoning
depth — use `reasoning_effort` for that). Configure it with `text_verbosity`
on any `openai-responses` endpoint:

```yaml
vendors:
  openai:
    protocol: openai-responses
    text_verbosity: low    # low | medium | high (empty = API default)
    endpoints:
      default:
        base_url: https://api.openai.com/v1
        model: gpt-5-codex
```

Valid values are `low`, `medium`, and `high`; the field is omitted from the
request when unset. Endpoints that predate the parameter (some
OpenAI-compatible relays) reject it as an unknown argument; ggcode detects that
error class and transparently retries the request without the hint.

## Reasoning Effort

Configure how much "thinking" the model does before responding. Supported by
`openai`, `anthropic`, and `gemini` protocols.

```yaml
vendors:
  openai:
    protocol: openai
    reasoning_effort: high    # low | medium | high
    endpoints:
      default:
        model: o3
  anthropic:
    protocol: anthropic
    reasoning_effort: high
    endpoints:
      default:
        model: claude-sonnet-4
  google:
    protocol: gemini
    reasoning_effort: high
    endpoints:
      default:
        model: gemini-2.5-pro
```

Effort levels:

| Level | OpenAI | Anthropic | Gemini |
|-------|--------|-----------|--------|
| `low` | `reasoning_effort: low` | `budget_tokens` ~5K | `thinkingBudget` ~25% |
| `medium` | `reasoning_effort: medium` | `budget_tokens` ~16K | `thinkingBudget` ~50% |
| `high` | `reasoning_effort: high` | `budget_tokens` ~32K | `thinkingBudget` ~75% |

When the effort level is empty (default), no reasoning parameters are sent and
the model uses its default behavior. If a model does not support reasoning, the
provider automatically retries without the parameter (no manual intervention needed).

### Thinking Mode (Anthropic)

Anthropic has two extended-thinking carriers, and the right one depends on the
model generation:

- **Adaptive thinking** (`thinking: {type: "adaptive"}` + top-level
  `output_config.effort`) — used automatically on Claude 4.6+ and 5.x models.
  This is the only mode that interleaves thinking between tool calls on these
  models, and effort (not `budget_tokens`) is its depth control.
- **Manual thinking** (`budget_tokens`) — used on the extended-thinking-only
  generation (Sonnet/Opus/Haiku 4.5 and earlier). With tools, ggcode attaches
  the `interleaved-thinking-2025-05-14` beta header so thinking can interleave
  between tool calls.

`thinking_mode` overrides the auto-detection per endpoint (`auto` is the
default; unknown models always fall back to the manual carrier):
## Context Editing (Anthropic)

Server-side context management: once Claude has processed a tool result or a
thinking block, the API itself can clear those blocks from the conversation on
later turns. Long agent sessions stay inside the context window and input-token
spend drops, with no client-side compaction involved. Supported by the
`anthropic` protocol (beta flag `context-management-2025-06-27`).

```yaml
vendors:
  anthropic:
    protocol: anthropic
    reasoning_effort: high
    endpoints:
      default:
        model: my-claude-gateway-model
        thinking_mode: adaptive    # auto (default) | manual | adaptive
```

Use `adaptive` when your endpoint fronts a Claude 4.6+/5.x model under a
renamed model ID, or `manual` to force `budget_tokens` on newer models.
    context_editing: all    # tool_results | thinking | all (empty = off)
    endpoints:
      default:
        model: claude-sonnet-4
```

Modes:

- `tool_results` — `clear_tool_uses_20250919`: older tool results are cleared
  after the input reaches the trigger threshold (API default 100k input tokens);
  the most recent 3 tool uses are preserved by default.
- `thinking` — `clear_thinking_20251015`: thinking blocks from earlier turns are
  cleared (the latest thinking turn is always kept).
- `all` — both strategies.

When the API applies an edit, the cleared block count and token savings are
surfaced as a system message in streaming output (and in debug logs for
non-streaming calls). Because edits are server-side, prompt cache prefixes
rewrite accordingly and no conversation history is removed from ggcode's local
session files.

## Tool Choice

Control whether the model is allowed to call tools. Supported by `openai`,
`anthropic`, and `gemini` protocols.

```yaml
vendors:
  openai:
    protocol: openai
    tool_choice: required    # auto | required | none
    endpoints:
      default:
        model: gpt-4o
```

| Value | Behavior |
|-------|----------|
| `auto` (default) | Model decides whether to call a tool |
| `required` | Force the model to call at least one tool |
| `none` | Disable all tool calls |

## Confidence (Logprobs)

Opt-in confidence telemetry for the `openai` and `gemini` protocols
(Anthropic does not expose token logprobs):

```yaml
vendors:
  openai:
    protocol: openai
    logprobs: true
    endpoints:
      default:
        model: gpt-4o
```

When enabled, ggcode requests token logprobs (`logprobs` + `top_logprobs: 1`
on OpenAI, `responseLogprobs` on Gemini) and reports the turn's mean token
logprob as a confidence signal (research: "Logprobs Know Uncertainty", ACM
KDD 2025 — token logprobs quantify model confidence and enable selective
escalation). The value is always written to the verbose debug log; turns with
mean logprob < -2.5 (~8% average token probability) additionally surface a
low-confidence notice in the UI so you can review potentially unreliable
output. Note that logprobs are not a calibrated probability and measured
accuracy degrades on out-of-distribution or paraphrased prompts — treat the
signal as advisory, not as truth.

Setting `tool_choice: required` is useful when you want to force the model into
agentic action (e.g., in autopilot mode). `none` is useful for pure
conversational responses without tool use.

## Strict Tool Use

Grammar-constrained tool inputs (structured outputs): the provider compiles a
tool's JSON Schema into a generation grammar, so `required` fields are always
present with correct types instead of being a semantic hint only. Supported by
`openai` and `anthropic` protocols; other protocols ignore the setting.

```yaml
vendors:
  anthropic:
    protocol: anthropic
    strict_tools: true                      # opt-in; off by default
    strict_tools_allow:                     # optional; default below
      - read_file
      - edit_file
      - write_file
      - run_command
    endpoints:
      default:
        model: claude-sonnet-4-5
```

| Field | Behavior |
|-------|----------|
| `strict_tools` (default off) | Send allowlisted tools with `strict: true` |
| `strict_tools_allow` (optional) | Tool allowlist; defaults to the four file/command tools above |

Tools whose schema has optional top-level fields are automatically skipped
(strict mode requires every top-level property to be required), and
`additionalProperties: false` is injected for you. Keep the allowlist short:
Anthropic compiles at most 20 strict tools per request, and schemas with deeply
optional fields cost more grammar complexity. Only enable this on first-party
endpoints — some OpenAI-compatible APIs reject `strict: true` with HTTP 400.

## Test Connectivity

Use `llm-probe` to verify your setup and list available models:

```bash
ggcode llm-probe
```

## Temperature and Sampling

Control the randomness of model output. Supported by `openai`, `anthropic`,
and `gemini` protocols. When unset, the provider's default applies.

```yaml
vendors:
  openai:
    protocol: openai
    temperature: 0.2          # 0.0-2.0 (0 = omit, use provider default)
    top_p: 0.95               # 0.0-1.0 (0 = omit)
    endpoints:
      default:
        model: gpt-4o
```

## Server-Side Tools (Built-in)

Declare provider-executed tools with `server_tools` — these run inside the
provider's API, never client-side, so they need no approval flow:

- **Anthropic**: `web_search_20250305`, `web_fetch_20250910`, and the
  Tool Search Tool variants `tool_search_tool_regex` /
  `tool_search_tool_bm25` (beta). The Tool Search Tool moves MCP tool
  schema discovery server-side: MCP schemas ride every request flagged
  `defer_loading` (kept out of the model's context until discovered), the
  API expands them on demand via `tool_reference`, and the required
  `anthropic-beta: advanced-tool-use-2025-11-20` header is attached
  automatically. This replaces ggcode's client-side `tool_search` meta-tool
  (which is disabled while the server variant is active) — configure one,
  not both:

```yaml
vendors:
  anthropic:
    protocol: anthropic
    server_tools:
      - type: tool_search_tool_regex
    endpoints:
      default:
        model: claude-sonnet-4-5
```

- **Gemini**: `google_search` (alias `web_search`) enables Google Search
  grounding; `url_context` lets the model fetch URLs named in the prompt
  (including PDFs/images). Grounding queries and source URLs are surfaced
  back into the conversation as `[grounding]` text blocks so the agent can
  cite its sources.
- **OpenAI (Responses API)**: `web_search` (aliases
  `web_search_2025_08_26`, legacy `web_search_preview`) runs hosted web
  search inside the Responses API. `web_search_call` items are surfaced as
  server-tool blocks and, because ggcode runs stateless (`store=false`),
  replayed verbatim on follow-up requests so the model keeps its search
  context across turns. `apply_patch` (sa-67) is declared in-API but
  executed **client-side**: the model emits `apply_patch_call` items with a
  V4A diff, ggcode applies them to the working tree through the hidden
  `apply_patch` executor (create/update/delete/move, monotonic hunk
  matching), and answers with `apply_patch_call_output` so the exchange
  replays losslessly in stateless history. Subject to the normal sandbox
  (`sandbox.paths` / approval) like any file-write tool.
  context across turns.
- **OpenAI Responses** (`protocol: openai-responses`): `code_interpreter`
  runs sandboxed Python inside an auto-provisioned container (`memory_limit`
  tier and seed `file_ids` optional) and `file_search` queries OpenAI vector
  stores (requires `vector_store_ids`). Interpreter transcripts, search hits,
  and generated-file citations are surfaced back as text blocks / `[file: …]`
  citation lines.

```yaml
vendors:
  google:
    protocol: gemini
    server_tools:
      - type: google_search
      - type: url_context
    endpoints:
      default:
        model: gemini-3-pro
  openai:
    protocol: openai-responses
    server_tools:
      - type: web_search
    endpoints:
      default:
        model: gpt-5.2
```

Notes for Gemini:

- Built-in tools are sent as separate tool entries, disjoint from regular
  function declarations (a Gemini API requirement).
- `tool_choice: required` is automatically downgraded to `auto` when built-in
  tools are configured — the Gemini API rejects the `ANY` + built-in
  combination.
- Unknown `server_tools` types are ignored (fail closed).

### Adaptive Sampling

When temperature is not explicitly set, ggcode automatically adjusts it per
LLM turn based on the current task phase:

| Phase | Temperature | When |
|-------|------------|------|
| Exploration | 0.4 | Reading and searching code |
| Code editing | 0.1 | Making file edits |
| Error recovery | 0.0 | Recovering from tool errors |
| Creative | 0.5 | Writing commit messages or docs |

This improves edit precision and error recovery while allowing creative
flexibility during exploration. Setting an explicit `temperature` in config
disables adaptive sampling. See `docs/design/adaptive-sampling.md` for details.
